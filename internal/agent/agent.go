// Package agent defines the AI investigator interface. The HolmesGPT implementation
// lives in agent/holmes; a direct LLM implementation could be added later.
// Agents are read-only: they only PROPOSE actions, which the core validates against
// the action registry and executes after human approval.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type SignalSummary struct {
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	Severity     string            `json:"severity"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"starts_at"`
	GeneratorURL string            `json:"generator_url,omitempty"`
}

// ActionType describes a registered executor the agent may propose.
type ActionType struct {
	Type         string          `json:"type"`
	Description  string          `json:"description"`
	Risk         string          `json:"risk"`
	ParamsSchema json.RawMessage `json:"params_schema"`
}

type IncidentContext struct {
	IncidentID       int               `json:"incident_id"`
	Title            string            `json:"title"`
	Status           string            `json:"status"`
	Severity         string            `json:"severity"`
	Cluster          string            `json:"cluster"`
	Namespace        string            `json:"namespace"`
	Labels           map[string]string `json:"labels"`
	Signals          []SignalSummary   `json:"signals"`
	URL              string            `json:"url"`
	OpenedAt         time.Time         `json:"opened_at"`
	PreviousAnalysis string            `json:"previous_analysis,omitempty"`
	ActionTypes      []ActionType      `json:"-"`
	// GitOps describes the Argo CD source of the namespace (repo files and their content), if any.
	GitOps string `json:"gitops,omitempty"`
	// Description adds free-form context (e.g. the list of incidents grouped by an alert storm).
	Description string `json:"description,omitempty"`
	// Model overrides the configured model for this call (e.g. a deeper model on explicit request).
	Model string `json:"-"`
}

type ProposedAction struct {
	Type      string         `json:"type"`
	Params    map[string]any `json:"params"`
	Rationale string         `json:"rationale"`
	Risk      string         `json:"risk"`
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

type InvestigationResult struct {
	Analysis        string // full markdown
	Summary         string // 1-3 sentences for cards
	RootCause       string
	Verdict         string // actionable | noise | unknown
	Confidence      float64
	ProposedActions []ProposedAction
	ToolCalls       json.RawMessage
	Conversation    json.RawMessage // opaque, passed back on Chat
	Model           string
	Usage           Usage
}

type ChatRequest struct {
	Incident     IncidentContext
	Conversation json.RawMessage
	Message      string
	Author       string
}

type ChatResult struct {
	Reply           string
	ProposedActions []ProposedAction
	ToolCalls       json.RawMessage
	Conversation    json.RawMessage
	Model           string
	Usage           Usage
}

// Progress is streamed while the agent works (tool calls, intermediate messages).
type Progress struct {
	Kind string `json:"kind"` // tool_call | tool_result | message
	Tool string `json:"tool,omitempty"`
	Text string `json:"text,omitempty"`
}

type Agent interface {
	Name() string
	Model() string
	Investigate(ctx context.Context, ic IncidentContext, progress func(Progress)) (*InvestigationResult, error)
	Chat(ctx context.Context, req ChatRequest, progress func(Progress)) (*ChatResult, error)
}

// IsBudgetExceeded reports whether err means the LLM budget or rate limit is exhausted
// (LiteLLM budget_exceeded / 429, Holmes rate-limit code 5204, ...). Such errors are final:
// the core fails open and pauses automatic investigations instead of retrying.
func IsBudgetExceeded(err error) bool {
	if err == nil {
		return false
	}
	var be interface{ BudgetExceeded() bool }
	if errors.As(err, &be) && be.BudgetExceeded() {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, k := range []string{"budget", "rate limit", "ratelimit", "rate_limit", "too many requests", "quota", "http 429"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// IsTransient reports errors worth retrying soon: the LLM gateway or provider was briefly unreachable
// or overloaded (connection errors, 5xx). Budget/rate-limit errors are never transient (they pause the agent).
func IsTransient(err error) bool {
	if err == nil || IsBudgetExceeded(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, k := range []string{"connection error", "connection refused", "connection reset", "internalservererror", "internal server error",
		"serviceunavailable", "service unavailable", "overloaded", "bad gateway", "gateway timeout", "http 500", "http 502", "http 503",
		"http 504", "http 529", "unexpected eof", "no such host", "i/o timeout"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// CorrelationCandidate is an open incident a new alert may belong to.
type CorrelationCandidate struct {
	IncidentID int
	Title      string
	OpenedAt   time.Time
	Summary    string // the incident's analysis summary ("" if not investigated yet)
	RootCause  string
	Signals    []SignalSummary
}

// CorrelationRequest asks whether a new alert is the same problem as one of the candidates.
type CorrelationRequest struct {
	Alert      IncidentContext
	Candidates []CorrelationCandidate
}

// CorrelationResult: SameAs is the candidate incident ID, 0 for a different problem.
type CorrelationResult struct {
	SameAs int
	Reason string
	Model  string
	Usage  Usage
}

// Correlator is implemented by agents that can group related alerts (one cheap call, no tools).
type Correlator interface {
	Correlate(ctx context.Context, req CorrelationRequest) (*CorrelationResult, error)
}

// ActionCheck is the agent's verdict on whether an approved action is still needed right before it runs.
type ActionCheck struct {
	StillNeeded bool
	Reason      string
	Model       string
	Usage       Usage
}

// ActionChecker is implemented by agents that can re-check an action against the live state.
type ActionChecker interface {
	// CheckAction looks at the current state (with tools) and says whether `action` (a precise
	// description: type, target, payload, why it was proposed) is still needed and safe to run now.
	CheckAction(ctx context.Context, ic IncidentContext, action string) (*ActionCheck, error)
}
