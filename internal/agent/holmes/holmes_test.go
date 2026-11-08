package holmes_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/agent/holmes"
	"github.com/xonoko/infra-monitor/internal/agent/holmes/holmestest"
	"github.com/xonoko/infra-monitor/internal/config"
)

var actionTypes = []agent.ActionType{
	{Type: "k8s.rollout_restart", Description: "Restart a workload", Risk: "medium",
		ParamsSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"name":{"type":"string"}}}`)},
	{Type: "alertmanager.silence", Description: "Silence alerts", Risk: "low", ParamsSchema: json.RawMessage(`{"type":"object"}`)},
}

func incident() agent.IncidentContext {
	return agent.IncidentContext{
		IncidentID: 42, Title: "KubePodCrashLooping shop", Status: "open", Severity: "warning",
		Cluster: "manager", Namespace: "shop", Labels: map[string]string{"alertname": "KubePodCrashLooping", "namespace": "shop"},
		Signals: []agent.SignalSummary{{Name: "KubePodCrashLooping", Status: "firing", Severity: "warning",
			Labels: map[string]string{"pod": "api-1"}, Annotations: map[string]string{"summary": "Pod is crash looping."},
			StartsAt: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC), GeneratorURL: "http://vmalert/alert?id=1"}},
		URL:         "https://infra-monitor.example.com/incidents/42",
		OpenedAt:    time.Date(2026, 11, 8, 10, 0, 30, 0, time.UTC),
		ActionTypes: actionTypes,
	}
}

func newClient(t *testing.T, srv *holmestest.Server, mod func(*config.AgentConfig), opts ...holmes.Option) *holmes.Client {
	cfg := config.AgentConfig{Enabled: true, Type: "holmes", URL: srv.URL, Model: "investigate", Timeout: 10 * time.Second, StructuredOutput: true}
	if mod != nil {
		mod(&cfg)
	}
	c, err := holmes.New(cfg, nil, opts...)
	require.NoError(t, err)
	return c
}

type progressLog struct {
	mu sync.Mutex
	ps []agent.Progress
}

func (p *progressLog) add(x agent.Progress) { p.mu.Lock(); p.ps = append(p.ps, x); p.mu.Unlock() }

func TestInvestigateStreamingStructured(t *testing.T) {
	srv := holmestest.NewServer(t)
	srv.APIKey = "secret"
	srv.Enqueue(holmestest.InvestigationResponse(holmestest.Investigation{
		Verdict: "actionable", Summary: "api crashes on missing config.", RootCause: "ConfigMap key removed",
		Confidence: 0.85, Analysis: "## Findings\nThe pod panics.",
		ProposedActions: []holmestest.ProposedAction{holmestest.Action("k8s.rollout_restart", map[string]any{"kind": "deployment", "name": "api"}, "pick up config", "medium")},
	}))
	c := newClient(t, srv, func(c *config.AgentConfig) { c.APIKey = "secret" })
	var pl progressLog
	res, err := c.Investigate(context.Background(), incident(), pl.add)
	require.NoError(t, err)

	require.Equal(t, "actionable", res.Verdict)
	require.Equal(t, "api crashes on missing config.", res.Summary)
	require.Equal(t, "ConfigMap key removed", res.RootCause)
	require.InDelta(t, 0.85, res.Confidence, 1e-9)
	require.Contains(t, res.Analysis, "The pod panics")
	require.Len(t, res.ProposedActions, 1)
	require.Equal(t, "k8s.rollout_restart", res.ProposedActions[0].Type)
	require.Equal(t, map[string]any{"kind": "deployment", "name": "api"}, res.ProposedActions[0].Params)
	require.Equal(t, 1200, res.Usage.PromptTokens)
	require.Equal(t, 300, res.Usage.CompletionTokens)
	require.Equal(t, "investigate", res.Model)
	require.NotEmpty(t, res.Conversation)

	var tools []map[string]any
	require.NoError(t, json.Unmarshal(res.ToolCalls, &tools))
	require.Len(t, tools, 2)
	require.Equal(t, "kubectl_describe", tools[0]["name"])
	require.Equal(t, "success", tools[0]["status"])

	kinds := []string{}
	for _, p := range pl.ps {
		kinds = append(kinds, p.Kind)
	}
	require.Equal(t, []string{"tool_call", "tool_result", "message", "tool_call", "tool_result"}, kinds)
	require.Equal(t, "kubectl_logs", pl.ps[3].Tool)

	req := srv.LastRequest()
	require.Equal(t, "secret", req.Header.Get("X-API-Key"))
	require.True(t, req.Body.Stream)
	require.Equal(t, "investigate", req.Body.Model)
	require.False(t, req.Body.EnableToolApproval)
	require.Contains(t, req.Body.Ask, "Incident #42")
	require.Contains(t, req.Body.Ask, "pod=api-1")
	require.Contains(t, req.Body.Ask, "Pod is crash looping.")
	require.Contains(t, req.Body.Ask, "http://vmalert/alert?id=1")
	require.Contains(t, req.Body.AdditionalSystemPrompt, "k8s.rollout_restart")
	require.Contains(t, req.Body.AdditionalSystemPrompt, "read-only")
	var rf map[string]any
	require.NoError(t, json.Unmarshal(req.Body.ResponseFormat, &rf))
	require.Equal(t, "json_schema", rf["type"])
	js := rf["json_schema"].(map[string]any)
	require.Equal(t, true, js["strict"])
	enum := js["schema"].(map[string]any)["properties"].(map[string]any)["proposed_actions"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["type"].(map[string]any)["enum"]
	require.Equal(t, []any{"alertmanager.silence", "k8s.rollout_restart"}, enum)
}

func TestInvestigateNonStreamFallbackAndFenced(t *testing.T) {
	srv := holmestest.NewServer(t)
	// Server answers JSON even though we asked for a stream; analysis has a fenced block.
	srv.Enqueue(holmestest.Response{ForceJSON: true, Analysis: "The disk is filling up because of old WAL files.\n\n```json\n" +
		`{"verdict": "Actionable", "summary": "Disk almost full.", "confidence": "70%", "proposed_actions": [{"type":"alertmanager.silence","params":{"duration":"1h"},"rationale":"noise","risk":"LOW"}]}` +
		"\n```\nLet me know if you need more."})
	c := newClient(t, srv, func(c *config.AgentConfig) { c.StructuredOutput = false })
	res, err := c.Investigate(context.Background(), incident(), nil)
	require.NoError(t, err)
	require.Equal(t, "actionable", res.Verdict)
	require.Equal(t, "Disk almost full.", res.Summary)
	require.InDelta(t, 0.7, res.Confidence, 1e-9)
	require.Contains(t, res.Analysis, "old WAL files")
	require.NotContains(t, res.Analysis, "```json")
	require.Len(t, res.ProposedActions, 1)
	require.Equal(t, "low", res.ProposedActions[0].Risk)
	require.Equal(t, map[string]any{"duration": "1h"}, res.ProposedActions[0].Params)
	req := srv.LastRequest()
	require.Empty(t, req.Body.ResponseFormat)
	require.Contains(t, req.Body.AdditionalSystemPrompt, "```json")
}

func TestInvestigateGarbageFallback(t *testing.T) {
	srv := holmestest.NewServer(t)
	srv.Enqueue(holmestest.Response{Analysis: "# Analysis\nThe node ran out of memory. Several pods were evicted. Nothing else looks wrong. More text here."})
	c := newClient(t, srv, nil)
	res, err := c.Investigate(context.Background(), incident(), nil)
	require.NoError(t, err)
	require.Equal(t, "unknown", res.Verdict)
	require.Contains(t, res.Analysis, "ran out of memory")
	require.True(t, strings.HasPrefix(res.Summary, "Analysis The node ran out of memory."), res.Summary)
	require.Empty(t, res.ProposedActions)
}

func TestChatHistoryRoundTrip(t *testing.T) {
	srv := holmestest.NewServer(t)
	srv.Enqueue(holmestest.InvestigationResponse(holmestest.Investigation{Verdict: "noise", Summary: "ok", Analysis: "ok"}))
	srv.Enqueue(holmestest.Response{Analysis: holmestest.JSON(holmestest.Chat{
		Reply:           "It restarted **3 times** in the last hour.",
		ProposedActions: []holmestest.ProposedAction{holmestest.Action("k8s.rollout_restart", map[string]any{"kind": "deployment", "name": "api"}, "r", "medium")},
	})})
	c := newClient(t, srv, nil)
	ctx := context.Background()
	inv, err := c.Investigate(ctx, incident(), nil)
	require.NoError(t, err)

	res, err := c.Chat(ctx, agent.ChatRequest{Incident: incident(), Conversation: inv.Conversation, Message: "how often did it restart?", Author: "Lucas"}, nil)
	require.NoError(t, err)
	require.Equal(t, "It restarted **3 times** in the last hour.", res.Reply)
	require.Len(t, res.ProposedActions, 1)
	require.NotEmpty(t, res.Conversation)

	req := srv.LastRequest()
	require.JSONEq(t, string(inv.Conversation), string(req.Body.ConversationHistory))
	require.True(t, strings.HasPrefix(req.Body.Ask, "Lucas: how often did it restart?"), req.Body.Ask)
	require.NotContains(t, req.Body.Ask, "Incident #42", "context is in the history already")
	require.Contains(t, req.Body.Ask, "k8s.rollout_restart")
}

func TestChatWithoutHistoryIncludesContext(t *testing.T) {
	srv := holmestest.NewServer(t)
	srv.Enqueue(holmestest.Response{Analysis: "Plain text reply without JSON."}, holmestest.Response{Analysis: "second"})
	c := newClient(t, srv, nil)
	for _, conv := range []json.RawMessage{nil, json.RawMessage("null")} {
		res, err := c.Chat(context.Background(), agent.ChatRequest{Incident: incident(), Conversation: conv, Message: "what is wrong?", Author: "Lucas"}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, res.Reply)
		req := srv.LastRequest()
		require.Empty(t, req.Body.ConversationHistory)
		require.Contains(t, req.Body.Ask, "Incident #42")
		require.Contains(t, req.Body.Ask, "Lucas: what is wrong?")
	}
}

func TestErrors(t *testing.T) {
	srv := holmestest.NewServer(t)
	srv.APIKey = "right"
	c := newClient(t, srv, func(c *config.AgentConfig) { c.APIKey = "wrong" })
	_, err := c.Investigate(context.Background(), incident(), nil)
	var he *holmes.HTTPError
	require.ErrorAs(t, err, &he)
	require.Equal(t, 401, he.Status)
	require.Equal(t, "Invalid or missing API key", he.Detail)
	require.False(t, he.Retryable())
	require.NotContains(t, err.Error(), "wrong")

	srv2 := holmestest.NewServer(t)
	srv2.Enqueue(holmestest.Response{Status: 500, Body: `{"detail":"litellm unavailable"}`})
	_, err = newClient(t, srv2, nil).Investigate(context.Background(), incident(), nil)
	require.ErrorAs(t, err, &he)
	require.True(t, he.Retryable())
	require.Contains(t, err.Error(), "litellm unavailable")

	srv2.Enqueue(holmestest.Response{Events: []holmestest.Event{{Name: "error", Data: map[string]any{"description": "Rate limit", "error_code": 5204, "msg": "rate limited", "success": false}}}, NoAnswerEnd: true})
	_, err = newClient(t, srv2, nil).Investigate(context.Background(), incident(), nil)
	var se *holmes.StreamError
	require.ErrorAs(t, err, &se)
	require.Equal(t, 5204, se.Code)
	require.True(t, se.Retryable())

	srv2.Enqueue(holmestest.Response{NoAnswerEnd: true})
	_, err = newClient(t, srv2, nil).Investigate(context.Background(), incident(), nil)
	require.ErrorContains(t, err, "without ai_answer_end")
}

func TestContextCancellation(t *testing.T) {
	srv := holmestest.NewServer(t)
	srv.Enqueue(holmestest.Response{Delay: 5 * time.Second, Analysis: "late"})
	c := newClient(t, srv, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Investigate(ctx, incident(), nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded), err)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestNonStreamingOption(t *testing.T) {
	srv := holmestest.NewServer(t)
	srv.Enqueue(holmestest.Response{Analysis: holmestest.JSON(holmestest.Investigation{Verdict: "noise", Summary: "Expected batch job.", Confidence: 0.9}),
		ToolCalls: []map[string]any{{"tool_name": "kubectl_get"}}})
	c := newClient(t, srv, nil, holmes.WithStreaming(false))
	res, err := c.Investigate(context.Background(), incident(), nil)
	require.NoError(t, err)
	require.False(t, srv.LastRequest().Body.Stream)
	require.Equal(t, "noise", res.Verdict)
	require.Equal(t, "Expected batch job.", res.Analysis, "analysis falls back to summary/root cause text")
	require.Contains(t, string(res.ToolCalls), "kubectl_get")
}

func TestNewValidatesURL(t *testing.T) {
	_, err := holmes.New(config.AgentConfig{URL: "::"}, nil)
	require.Error(t, err)
}
