// Package holmestest provides a fake HolmesGPT server (POST /api/chat) for tests.
// Responses are scripted in order; the server records every request.
package holmestest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Event is one SSE event of a streamed answer.
type Event struct {
	Name string
	Data any
}

// Response is a scripted answer.
type Response struct {
	// Status overrides 200 (e.g. 500 with Body `{"detail":"boom"}`).
	Status int
	Body   string
	// Analysis is the final answer text (a JSON string for structured output).
	Analysis            string
	ConversationHistory any
	ToolCalls           any
	// Events precede ai_answer_end in stream mode (tool calls, ai messages, or an error).
	Events []Event
	// Delay before answering (to test timeouts/cancellation).
	Delay time.Duration
	// NoAnswerEnd: stream ends without ai_answer_end.
	NoAnswerEnd bool
	// ForceJSON answers non-stream even when the request asks for a stream.
	ForceJSON bool
	Usage     *Usage
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Request is a recorded call.
type Request struct {
	Header http.Header
	Body   ChatBody
	Raw    []byte
}

// ChatBody mirrors the /api/chat request.
type ChatBody struct {
	Ask                    string          `json:"ask"`
	Model                  string          `json:"model"`
	ConversationHistory    json.RawMessage `json:"conversation_history"`
	Stream                 bool            `json:"stream"`
	ResponseFormat         json.RawMessage `json:"response_format"`
	AdditionalSystemPrompt string          `json:"additional_system_prompt"`
	EnableToolApproval     bool            `json:"enable_tool_approval"`
}

type Server struct {
	*httptest.Server
	// APIKey, when set, must match X-API-Key (else 401 like Holmes).
	APIKey string

	mu       sync.Mutex
	script   []Response
	requests []Request
}

// NewServer starts a fake Holmes; it is closed at test cleanup.
func NewServer(t testing.TB) *Server {
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// Enqueue scripts the next responses (FIFO). When empty, a default "unknown" answer is returned.
func (s *Server) Enqueue(rs ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script = append(s.script, rs...)
}

// Requests returns the recorded requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// LastRequest returns the most recent request.
func (s *Server) LastRequest() Request {
	r := s.Requests()
	if len(r) == 0 {
		return Request{}
	}
	return r[len(r)-1]
}

// Investigation is a structured investigation answer.
type Investigation struct {
	Verdict         string           `json:"verdict"`
	Summary         string           `json:"summary"`
	RootCause       string           `json:"root_cause"`
	Confidence      float64          `json:"confidence"`
	Analysis        string           `json:"analysis"`
	ProposedActions []ProposedAction `json:"proposed_actions"`
}

// ProposedAction uses params_json like the structured schema infra-monitor sends.
type ProposedAction struct {
	Type       string `json:"type"`
	ParamsJSON string `json:"params_json"`
	Rationale  string `json:"rationale"`
	Risk       string `json:"risk"`
}

// Action builds a ProposedAction from a params object.
func Action(typ string, params map[string]any, rationale, risk string) ProposedAction {
	b, _ := json.Marshal(params)
	return ProposedAction{Type: typ, ParamsJSON: string(b), Rationale: rationale, Risk: risk}
}

// Chat is a structured chat answer.
type Chat struct {
	Reply           string           `json:"reply"`
	ProposedActions []ProposedAction `json:"proposed_actions"`
}

// JSON marshals v (helper for Analysis).
func JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// History returns a minimal conversation history ending with the assistant's answer.
func History(turns ...string) []map[string]string {
	h := []map[string]string{{"role": "system", "content": "You are HolmesGPT"}}
	for i, t := range turns {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		h = append(h, map[string]string{"role": role, "content": t})
	}
	return h
}

// InvestigationResponse is a streamed answer with two tool calls.
func InvestigationResponse(inv Investigation) Response {
	return Response{
		Analysis:            JSON(inv),
		ConversationHistory: History("investigate", JSON(inv)),
		Events: []Event{
			{Name: "start_tool_calling", Data: map[string]any{"tool_name": "kubectl_describe", "id": "t1"}},
			{Name: "tool_calling_result", Data: map[string]any{"tool_call_id": "t1", "role": "tool", "name": "kubectl_describe",
				"description": "kubectl describe pod api-1 -n shop", "result": map[string]any{"status": "success", "data": "State: CrashLoopBackOff", "error": nil, "params": map[string]any{"pod": "api-1"}}}},
			{Name: "ai_message", Data: map[string]any{"content": "Pod is crash looping, checking logs", "reasoning": nil, "metadata": map[string]any{}}},
			{Name: "start_tool_calling", Data: map[string]any{"tool_name": "kubectl_logs", "id": "t2"}},
			{Name: "tool_calling_result", Data: map[string]any{"tool_call_id": "t2", "role": "tool", "name": "kubectl_logs",
				"description": "kubectl logs api-1 -n shop", "result": map[string]any{"status": "success", "data": "panic: config missing", "error": nil, "params": map[string]any{}}}},
		},
		Usage: &Usage{PromptTokens: 1200, CompletionTokens: 300, TotalTokens: 1500},
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
		http.NotFound(w, r)
		return
	}
	if s.APIKey != "" && r.Header.Get("X-API-Key") != s.APIKey {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Invalid or missing API key"}`))
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, `{"detail":"invalid json"}`, http.StatusUnprocessableEntity)
		return
	}
	var body ChatBody
	_ = json.Unmarshal(raw, &body)
	s.mu.Lock()
	s.requests = append(s.requests, Request{Header: r.Header.Clone(), Body: body, Raw: raw})
	var resp Response
	if len(s.script) > 0 {
		resp = s.script[0]
		s.script = s.script[1:]
	} else {
		resp = Response{Analysis: JSON(Investigation{Verdict: "unknown", Summary: "Nothing found.", Analysis: "Nothing found."})}
	}
	s.mu.Unlock()

	if resp.Delay > 0 {
		select {
		case <-time.After(resp.Delay):
		case <-r.Context().Done():
			return
		}
	}
	if resp.Status != 0 && resp.Status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.Status)
		_, _ = w.Write([]byte(resp.Body))
		return
	}
	history := resp.ConversationHistory
	if history == nil {
		history = History(body.Ask, resp.Analysis)
	}
	final := map[string]any{"analysis": resp.Analysis, "conversation_history": history}
	if resp.Usage != nil {
		final["metadata"] = map[string]any{"usage": resp.Usage}
	}
	if !body.Stream || resp.ForceJSON {
		final["tool_calls"] = resp.ToolCalls
		if resp.ToolCalls == nil {
			final["tool_calls"] = []any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(final)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	write := func(name string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		if fl != nil {
			fl.Flush()
		}
	}
	for _, e := range resp.Events {
		write(e.Name, e.Data)
	}
	if !resp.NoAnswerEnd {
		write("ai_answer_end", final)
	}
}
