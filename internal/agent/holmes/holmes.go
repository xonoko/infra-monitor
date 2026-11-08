// Package holmes implements agent.Agent with HolmesGPT's HTTP API (POST /api/chat).
// Holmes is read-only: it investigates with its own tools and only proposes actions,
// which infra-monitor validates and runs after approval.
package holmes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// Client is a HolmesGPT HTTP client.
type Client struct {
	cfg    config.AgentConfig
	log    *slog.Logger
	http   *http.Client
	url    string
	stream bool
}

var _ agent.Agent = (*Client)(nil)

// Option tweaks the client (tests).
type Option func(*Client)

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(c *http.Client) Option { return func(cl *Client) { cl.http = c } }

// WithStreaming toggles SSE streaming (default on).
func WithStreaming(on bool) Option { return func(cl *Client) { cl.stream = on } }

// New creates a Holmes client.
func New(cfg config.AgentConfig, log *slog.Logger, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(cfg.URL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("holmes: invalid url %q", cfg.URL)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	// No client timeout: the per-call context carries cfg.Timeout (investigations take minutes).
	c := &Client{cfg: cfg, log: log, http: telemetry.HTTPClient(0), url: u.String() + "/api/chat", stream: true}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func (c *Client) Name() string  { return "holmes" }
func (c *Client) Model() string { return c.cfg.Model }

func (c *Client) model(override string) string {
	if override != "" {
		return override
	}
	return c.cfg.Model
}

// HTTPError is a non-2xx answer from Holmes.
type HTTPError struct {
	Status int
	Detail string
}

func (e *HTTPError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("holmes: HTTP %d", e.Status)
	}
	return fmt.Sprintf("holmes: HTTP %d: %s", e.Status, e.Detail)
}

// Retryable reports whether retrying may help (5xx, 429).
func (e *HTTPError) Retryable() bool {
	return e.Status >= 500 || e.Status == http.StatusTooManyRequests
}

// BudgetExceeded: 429, or a budget error relayed from LiteLLM.
func (e *HTTPError) BudgetExceeded() bool {
	d := strings.ToLower(e.Detail)
	return e.Status == http.StatusTooManyRequests || strings.Contains(d, "budget") || strings.Contains(d, "rate limit") || strings.Contains(d, "ratelimit")
}

// StreamError is an `error` event from the Holmes stream.
type StreamError struct {
	Code int
	Msg  string
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("holmes: stream error %d: %s", e.Code, e.Msg)
}

// Retryable: 5204 is Holmes' rate-limit code.
func (e *StreamError) Retryable() bool { return e.Code == 5204 }

// BudgetExceeded: rate limit (5204) or a budget error in the message.
func (e *StreamError) BudgetExceeded() bool {
	m := strings.ToLower(e.Msg)
	return e.Code == 5204 || strings.Contains(m, "budget") || strings.Contains(m, "rate limit")
}

type chatRequest struct {
	Ask                    string          `json:"ask"`
	Model                  string          `json:"model,omitempty"`
	ConversationHistory    json.RawMessage `json:"conversation_history,omitempty"`
	Stream                 bool            `json:"stream"`
	ResponseFormat         any             `json:"response_format,omitempty"`
	AdditionalSystemPrompt string          `json:"additional_system_prompt,omitempty"`
	EnableToolApproval     bool            `json:"enable_tool_approval"`
}

type chatResponse struct {
	Analysis            string          `json:"analysis"`
	ConversationHistory json.RawMessage `json:"conversation_history"`
	ToolCalls           json.RawMessage `json:"tool_calls"`
	Metadata            *metadata       `json:"metadata"`
}

type metadata struct {
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Investigate runs a root-cause investigation.
func (c *Client) Investigate(ctx context.Context, ic agent.IncidentContext, progress func(agent.Progress)) (*agent.InvestigationResult, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "holmes.investigate", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.Int("incident.id", ic.IncidentID), attribute.String("agent.model", c.model(ic.Model))))
	defer span.End()
	req := chatRequest{
		Ask:                    investigationAsk(ic),
		Model:                  c.model(ic.Model),
		AdditionalSystemPrompt: investigationSystemPrompt(ic.ActionTypes, c.cfg.StructuredOutput, c.cfg.ExtraPrompt),
	}
	if c.cfg.StructuredOutput {
		req.ResponseFormat = responseFormat("infra_monitor_investigation", investigationSchema(ic.ActionTypes))
	}
	resp, toolCalls, err := c.call(ctx, req, progress)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	res := parseInvestigation(resp.Analysis)
	res.Conversation = resp.ConversationHistory
	res.ToolCalls = toolCalls
	res.Model = c.model(ic.Model)
	if resp.Metadata != nil {
		res.Usage = agent.Usage{PromptTokens: resp.Metadata.Usage.PromptTokens, CompletionTokens: resp.Metadata.Usage.CompletionTokens}
	}
	span.SetAttributes(attribute.String("agent.verdict", res.Verdict), attribute.Float64("agent.confidence", res.Confidence),
		attribute.Int("agent.proposed_actions", len(res.ProposedActions)),
		attribute.Int("agent.prompt_tokens", res.Usage.PromptTokens), attribute.Int("agent.completion_tokens", res.Usage.CompletionTokens))
	return res, nil
}

// Chat answers a follow-up question in the incident conversation.
func (c *Client) Chat(ctx context.Context, req agent.ChatRequest, progress func(agent.Progress)) (*agent.ChatResult, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "holmes.chat", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.Int("incident.id", req.Incident.IncidentID), attribute.String("agent.model", c.cfg.Model),
		attribute.Bool("agent.has_history", len(req.Conversation) > 0 && string(req.Conversation) != "null")))
	defer span.End()
	hasHistory := len(req.Conversation) > 0 && string(req.Conversation) != "null"
	cr := chatRequest{
		Ask:                    chatAsk(req, hasHistory),
		Model:                  c.model(req.Incident.Model),
		AdditionalSystemPrompt: chatSystemPrompt(req.Incident.ActionTypes, c.cfg.StructuredOutput, c.cfg.ExtraPrompt),
	}
	if hasHistory {
		cr.ConversationHistory = req.Conversation
	}
	if c.cfg.StructuredOutput {
		cr.ResponseFormat = responseFormat("infra_monitor_chat", chatSchema(req.Incident.ActionTypes))
	}
	resp, toolCalls, err := c.call(ctx, cr, progress)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	res := parseChat(resp.Analysis)
	res.Conversation = resp.ConversationHistory
	res.ToolCalls = toolCalls
	res.Model = c.model(req.Incident.Model)
	if resp.Metadata != nil {
		res.Usage = agent.Usage{PromptTokens: resp.Metadata.Usage.PromptTokens, CompletionTokens: resp.Metadata.Usage.CompletionTokens}
	}
	span.SetAttributes(attribute.Int("agent.proposed_actions", len(res.ProposedActions)))
	return res, nil
}

// call POSTs to /api/chat, streaming when enabled, and returns the final answer and collected tool calls.
func (c *Client) call(ctx context.Context, req chatRequest, progress func(agent.Progress)) (*chatResponse, json.RawMessage, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}
	if progress == nil {
		progress = func(agent.Progress) {}
	}
	req.Stream = c.stream
	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.stream {
		hr.Header.Set("Accept", "text/event-stream, application/json")
	} else {
		hr.Header.Set("Accept", "application/json")
	}
	if c.cfg.APIKey != "" {
		hr.Header.Set("X-API-Key", c.cfg.APIKey)
	}
	start := time.Now()
	resp, err := c.http.Do(hr)
	if err != nil {
		return nil, nil, fmt.Errorf("holmes: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, nil, readHTTPError(resp)
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var out *chatResponse
	var tools json.RawMessage
	if ct == "text/event-stream" {
		out, tools, err = readStream(resp.Body, progress)
	} else {
		out, err = readJSON(resp.Body)
		if err == nil {
			tools = out.ToolCalls
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("holmes: %w", ctx.Err())
		}
		return nil, nil, err
	}
	c.log.DebugContext(ctx, "holmes answered", "duration", time.Since(start), "stream", ct == "text/event-stream", "analysis_len", len(out.Analysis))
	return out, tools, nil
}

func readHTTPError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	e := &HTTPError{Status: resp.StatusCode}
	var detail struct {
		Detail any `json:"detail"`
	}
	if json.Unmarshal(data, &detail) == nil && detail.Detail != nil {
		switch d := detail.Detail.(type) {
		case string:
			e.Detail = d
		default:
			b, _ := json.Marshal(d)
			e.Detail = string(b)
		}
	} else {
		e.Detail = strings.TrimSpace(string(data))
	}
	if len(e.Detail) > 500 {
		e.Detail = e.Detail[:500]
	}
	return e
}

func readJSON(r io.Reader) (*chatResponse, error) {
	var out chatResponse
	if err := json.NewDecoder(io.LimitReader(r, 64<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("holmes: decode response: %w", err)
	}
	return &out, nil
}

type toolCall struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
	Params      any    `json:"params,omitempty"`
	Result      string `json:"result,omitempty"`
}

type toolResultEvent struct {
	ToolCallID  string `json:"tool_call_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Result      struct {
		Status string `json:"status"`
		Data   any    `json:"data"`
		Error  any    `json:"error"`
		Params any    `json:"params"`
	} `json:"result"`
}

// readStream parses Holmes' SSE stream until ai_answer_end.
func readStream(r io.Reader, progress func(agent.Progress)) (*chatResponse, json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	var event string
	var data strings.Builder
	var tools []toolCall
	var final *chatResponse
	dispatch := func() error {
		defer func() { event = ""; data.Reset() }()
		if data.Len() == 0 && event == "" {
			return nil
		}
		payload := []byte(data.String())
		switch event {
		case "start_tool_calling":
			var e struct {
				ToolName string `json:"tool_name"`
				ID       string `json:"id"`
			}
			_ = json.Unmarshal(payload, &e)
			progress(agent.Progress{Kind: "tool_call", Tool: e.ToolName})
		case "tool_calling_result":
			var e toolResultEvent
			if err := json.Unmarshal(payload, &e); err != nil {
				return nil
			}
			tc := toolCall{ID: e.ToolCallID, Name: e.Name, Description: e.Description, Status: e.Result.Status, Params: e.Result.Params}
			if e.Result.Error != nil {
				tc.Error = truncate(stringify(e.Result.Error), 300)
			}
			tc.Result = truncate(stringify(e.Result.Data), 300)
			tools = append(tools, tc)
			text := e.Description
			if text == "" {
				text = e.Name
			}
			if tc.Status != "" && tc.Status != "success" {
				text += " (" + tc.Status + ")"
			}
			progress(agent.Progress{Kind: "tool_result", Tool: e.Name, Text: truncate(text, 300)})
		case "ai_message":
			var e struct {
				Content *string `json:"content"`
			}
			_ = json.Unmarshal(payload, &e)
			if e.Content != nil && strings.TrimSpace(*e.Content) != "" {
				progress(agent.Progress{Kind: "message", Text: truncate(*e.Content, 1000)})
			}
		case "ai_answer_end":
			var e chatResponse
			if err := json.Unmarshal(payload, &e); err != nil {
				return fmt.Errorf("holmes: decode ai_answer_end: %w", err)
			}
			final = &e
		case "approval_required":
			return errors.New("holmes: tool approval required but infra-monitor runs Holmes without tool approval (enable_tool_approval=false); check the Holmes toolset config")
		case "error":
			var e struct {
				Description string `json:"description"`
				ErrorCode   int    `json:"error_code"`
				Msg         string `json:"msg"`
			}
			_ = json.Unmarshal(payload, &e)
			msg := e.Msg
			if e.Description != "" {
				if msg != "" {
					msg += ": "
				}
				msg += e.Description
			}
			if msg == "" {
				msg = string(payload)
			}
			return &StreamError{Code: e.ErrorCode, Msg: msg}
		}
		return nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := dispatch(); err != nil {
				return nil, nil, err
			}
			if final != nil {
				return final, marshalTools(tools), nil
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("holmes: read stream: %w", err)
	}
	if err := dispatch(); err != nil {
		return nil, nil, err
	}
	if final == nil {
		return nil, nil, errors.New("holmes: stream ended without ai_answer_end")
	}
	return final, marshalTools(tools), nil
}

func marshalTools(t []toolCall) json.RawMessage {
	if len(t) == 0 {
		return nil
	}
	b, _ := json.Marshal(t)
	return b
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
