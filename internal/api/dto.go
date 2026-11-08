package api

import (
	"encoding/json"
	"time"

	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/incident"
)

type UserRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Incident struct {
	ID                 int               `json:"id"`
	Title              string            `json:"title"`
	Status             string            `json:"status" enum:"open,acknowledged,resolved,closed"`
	Severity           string            `json:"severity"`
	Cluster            string            `json:"cluster"`
	Namespace          string            `json:"namespace"`
	Source             string            `json:"source"`
	Verdict            string            `json:"verdict" enum:"actionable,noise,unknown"`
	Summary            string            `json:"summary"`
	Muted              bool              `json:"muted"`
	Internal           bool              `json:"internal"`
	Labels             map[string]string `json:"labels"`
	OpenedAt           time.Time         `json:"opened_at"`
	AcknowledgedAt     *time.Time        `json:"acknowledged_at,omitempty"`
	ResolvedAt         *time.Time        `json:"resolved_at,omitempty"`
	LastSignalAt       time.Time         `json:"last_signal_at"`
	DurationSeconds    int64             `json:"duration_seconds"`
	SignalCount        int               `json:"signal_count"`
	FiringCount        int               `json:"firing_count"`
	PendingActions     int               `json:"pending_actions"`
	ReopenCount        int               `json:"reopen_count"`
	InvestigationState string            `json:"investigation_state" enum:"none,pending,running,done,failed,skipped"`
	NotifyChannels     []string          `json:"notify_channels"`
	Assignee           *UserRef          `json:"assignee,omitempty"`
	TraceID            string            `json:"trace_id"`
	URL                string            `json:"url"`
}

func toIncident(svc *incident.Service, inc *ent.Incident, c incident.Counts) Incident {
	end := svc.Now()
	if inc.ResolvedAt != nil {
		end = *inc.ResolvedAt
	}
	out := Incident{
		ID: inc.ID, Title: inc.Title, Status: string(inc.Status), Severity: inc.Severity, Cluster: inc.Cluster,
		Namespace: inc.Namespace, Source: inc.Source, Verdict: string(inc.Verdict), Summary: inc.Summary, Muted: inc.Muted,
		Internal: inc.Internal, Labels: inc.Labels, OpenedAt: inc.OpenedAt, AcknowledgedAt: inc.AcknowledgedAt,
		ResolvedAt: inc.ResolvedAt, LastSignalAt: inc.LastSignalAt, DurationSeconds: int64(end.Sub(inc.OpenedAt).Seconds()),
		SignalCount: c.Signals, FiringCount: c.Firing, PendingActions: c.PendingActions, ReopenCount: inc.ReopenCount,
		InvestigationState: string(inc.InvestigationState), NotifyChannels: inc.NotifyChannels, TraceID: inc.TraceID,
		URL: svc.IncidentURL(inc.ID),
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	if out.NotifyChannels == nil {
		out.NotifyChannels = []string{}
	}
	if a := inc.Edges.Assignee; a != nil {
		name := a.DisplayName
		if name == "" {
			name = a.Email
		}
		out.Assignee = &UserRef{ID: a.ID, Name: name}
	}
	return out
}

type Signal struct {
	ID           int               `json:"id"`
	Source       string            `json:"source"`
	Fingerprint  string            `json:"fingerprint"`
	Status       string            `json:"status" enum:"firing,resolved"`
	Name         string            `json:"name"`
	Severity     string            `json:"severity"`
	Cluster      string            `json:"cluster"`
	Namespace    string            `json:"namespace"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"starts_at"`
	EndsAt       *time.Time        `json:"ends_at,omitempty"`
	GeneratorURL string            `json:"generator_url"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
	FireCount    int               `json:"fire_count"`
}

func toSignal(s *ent.Signal) Signal {
	return Signal{ID: s.ID, Source: s.Source, Fingerprint: s.Fingerprint, Status: string(s.Status), Name: s.Name,
		Severity: s.Severity, Cluster: s.Cluster, Namespace: s.Namespace, Labels: s.Labels, Annotations: s.Annotations,
		StartsAt: s.StartsAt, EndsAt: s.EndsAt, GeneratorURL: s.GeneratorURL, LastSeenAt: s.LastSeenAt, FireCount: s.FireCount}
}

type Investigation struct {
	ID               int             `json:"id"`
	Status           string          `json:"status" enum:"pending,running,succeeded,failed,timed_out"`
	Trigger          string          `json:"trigger" enum:"auto,manual,action"`
	Agent            string          `json:"agent"`
	Model            string          `json:"model"`
	Analysis         string          `json:"analysis"`
	Summary          string          `json:"summary"`
	RootCause        string          `json:"root_cause"`
	Verdict          string          `json:"verdict"`
	Confidence       float64         `json:"confidence"`
	ToolCalls        json.RawMessage `json:"tool_calls"`
	Error            string          `json:"error"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	CreatedAt        time.Time       `json:"created_at"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	TraceID          string          `json:"trace_id"`
}

func toInvestigation(i *ent.Investigation) Investigation {
	tc := i.ToolCalls
	if len(tc) == 0 {
		tc = json.RawMessage("[]")
	}
	return Investigation{ID: i.ID, Status: string(i.Status), Trigger: string(i.Trigger), Agent: i.Agent, Model: i.Model,
		Analysis: i.Analysis, Summary: i.Summary, RootCause: i.RootCause, Verdict: string(i.Verdict), Confidence: i.Confidence,
		ToolCalls: tc, Error: i.Error, PromptTokens: i.PromptTokens, CompletionTokens: i.CompletionTokens,
		CreatedAt: i.CreatedAt, StartedAt: i.StartedAt, FinishedAt: i.FinishedAt, TraceID: i.TraceID}
}

type Message struct {
	ID          int       `json:"id"`
	IncidentID  int       `json:"incident_id"`
	Role        string    `json:"role" enum:"user,agent,system"`
	AuthorName  string    `json:"author_name"`
	Channel     string    `json:"channel"`
	ChannelName string    `json:"channel_name"`
	Content     string    `json:"content"`
	Status      string    `json:"status" enum:"done,pending,failed"`
	ActionIDs   []int     `json:"action_ids"`
	CreatedAt   time.Time `json:"created_at"`
}

func toMessage(m *ent.Message) Message {
	ids := m.ActionIds
	if ids == nil {
		ids = []int{}
	}
	return Message{ID: m.ID, IncidentID: m.IncidentID, Role: string(m.Role), AuthorName: m.AuthorName, Channel: m.Channel,
		ChannelName: m.ChannelName, Content: m.Content, Status: string(m.Status), ActionIDs: ids, CreatedAt: m.CreatedAt}
}

type Action struct {
	ID            int            `json:"id"`
	IncidentID    int            `json:"incident_id"`
	Type          string         `json:"type"`
	Params        map[string]any `json:"params"`
	Rationale     string         `json:"rationale"`
	Risk          string         `json:"risk" enum:"low,medium,high"`
	ProposedBy    string         `json:"proposed_by" enum:"agent,user,system"`
	Status        string         `json:"status" enum:"proposed,approved,rejected,running,succeeded,failed,expired"`
	Summary       string         `json:"summary"`
	DecidedByName string         `json:"decided_by_name"`
	DecidedVia    string         `json:"decided_via"`
	DecidedAt     *time.Time     `json:"decided_at,omitempty"`
	ExpiresAt     time.Time      `json:"expires_at"`
	StartedAt     *time.Time     `json:"started_at,omitempty"`
	FinishedAt    *time.Time     `json:"finished_at,omitempty"`
	Output        string         `json:"output"`
	Error         string         `json:"error"`
	CreatedAt     time.Time      `json:"created_at"`
}

func toAction(a *ent.Action) Action {
	p := a.Params
	if p == nil {
		p = map[string]any{}
	}
	return Action{ID: a.ID, IncidentID: a.IncidentID, Type: a.Type, Params: p, Rationale: a.Rationale, Risk: string(a.Risk),
		ProposedBy: string(a.ProposedBy), Status: string(a.Status), Summary: a.Summary, DecidedByName: a.DecidedByName,
		DecidedVia: a.DecidedVia, DecidedAt: a.DecidedAt, ExpiresAt: a.ExpiresAt, StartedAt: a.StartedAt,
		FinishedAt: a.FinishedAt, Output: a.Output, Error: a.Error, CreatedAt: a.CreatedAt}
}

type TimelineEvent struct {
	ID          int            `json:"id"`
	IncidentID  int            `json:"incident_id"`
	Type        string         `json:"type"`
	ActorType   string         `json:"actor_type" enum:"user,agent,system"`
	ActorName   string         `json:"actor_name"`
	Channel     string         `json:"channel"`
	ChannelName string         `json:"channel_name"`
	Payload     map[string]any `json:"payload"`
	TraceID     string         `json:"trace_id"`
	CreatedAt   time.Time      `json:"created_at"`
}

func toEvent(e *ent.TimelineEvent) TimelineEvent {
	p := e.Payload
	if p == nil {
		p = map[string]any{}
	}
	return TimelineEvent{ID: e.ID, IncidentID: e.IncidentID, Type: e.Type, ActorType: string(e.ActorType), ActorName: e.ActorName,
		Channel: e.Channel, ChannelName: e.ChannelName, Payload: p, TraceID: e.TraceID, CreatedAt: e.CreatedAt}
}

type Mute struct {
	ID                    int              `json:"id"`
	Matchers              []domain.Matcher `json:"matchers"`
	StartsAt              time.Time        `json:"starts_at"`
	EndsAt                time.Time        `json:"ends_at"`
	Reason                string           `json:"reason"`
	CreatedByName         string           `json:"created_by_name"`
	Active                bool             `json:"active"`
	IncidentID            *int             `json:"incident_id,omitempty"`
	AlertmanagerSilenceID string           `json:"alertmanager_silence_id"`
	CreatedAt             time.Time        `json:"created_at"`
}

func toMute(svc *incident.Service, m *ent.Mute) Mute {
	return Mute{ID: m.ID, Matchers: m.Matchers, StartsAt: m.StartsAt, EndsAt: m.EndsAt, Reason: m.Reason,
		CreatedByName: m.CreatedByName, Active: svc.MuteActive(m), IncidentID: m.IncidentID,
		AlertmanagerSilenceID: m.AlertmanagerSilenceID, CreatedAt: m.CreatedAt}
}

type User struct {
	ID          int        `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role" enum:"viewer,operator,admin"`
	Disabled    bool       `json:"disabled"`
	Auth        []string   `json:"auth"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func toUser(u *ent.User) User {
	methods := []string{}
	if u.PasswordHash != nil {
		methods = append(methods, "local")
	}
	if u.OidcSubject != nil {
		methods = append(methods, "oidc")
	}
	return User{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: string(u.Role), Disabled: u.Disabled,
		Auth: methods, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt}
}

type APIToken struct {
	ID         int        `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role" enum:"viewer,operator,admin"`
	UserID     *int       `json:"user_id,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toToken(t *ent.APIToken) APIToken {
	return APIToken{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Role: string(t.Role), UserID: t.UserID,
		LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt}
}
