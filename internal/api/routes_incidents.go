package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/incident"
)

type ListParams struct {
	Status    string    `query:"status" doc:"Comma-separated statuses (open,acknowledged,resolved,closed)"`
	Severity  string    `query:"severity" doc:"Comma-separated severities"`
	Verdict   string    `query:"verdict" doc:"Comma-separated verdicts"`
	Cluster   string    `query:"cluster"`
	Namespace string    `query:"namespace"`
	Q         string    `query:"q" doc:"Free text (title, summary, labels)"`
	Since     time.Time `query:"since" doc:"Active since (RFC 3339)"`
	Until     time.Time `query:"until" doc:"Active until (RFC 3339)"`
	Muted     string    `query:"muted" enum:"true,false," doc:"Filter muted incidents"`
}

func csv(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (p ListParams) filter() incident.ListFilter {
	f := incident.ListFilter{Status: csv(p.Status), Severity: csv(p.Severity), Verdict: csv(p.Verdict),
		Cluster: p.Cluster, Namespace: p.Namespace, Query: p.Q}
	if !p.Since.IsZero() {
		t := p.Since
		f.Since = &t
	}
	if !p.Until.IsZero() {
		t := p.Until
		f.Until = &t
	}
	switch p.Muted {
	case "true":
		v := true
		f.Muted = &v
	case "false":
		v := false
		f.Muted = &v
	}
	return f
}

type ListIncidentsInput struct {
	ListParams
	Limit  int `query:"limit" minimum:"0" maximum:"200" default:"50"`
	Cursor int `query:"cursor" doc:"next_cursor from the previous page"`
}

type ListIncidentsOutput struct {
	Body struct {
		Items      []Incident `json:"items"`
		NextCursor *int       `json:"next_cursor,omitempty"`
		Total      int        `json:"total"`
	}
}

type HistogramInput struct {
	ListParams
	Buckets int `query:"buckets" minimum:"0" maximum:"500" default:"60"`
}

type HistogramBucket struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Critical int       `json:"critical"`
	Warning  int       `json:"warning"`
	Info     int       `json:"info"`
	Other    int       `json:"other"`
}

type HistogramOutput struct {
	Body struct {
		Buckets []HistogramBucket `json:"buckets"`
	}
}

type IDInput struct {
	ID int `path:"id"`
}

type IncidentDetail struct {
	Incident
	LatestInvestigation *Investigation `json:"latest_investigation,omitempty"`
	Cost                Cost           `json:"cost" doc:"Estimated AI token cost of this incident"`
}

type IncidentOutput struct{ Body Incident }
type IncidentDetailOutput struct{ Body IncidentDetail }

type TimelineInput struct {
	ID      int `path:"id"`
	AfterID int `query:"after_id"`
}

type ItemsOutput[T any] struct {
	Body struct {
		Items []T `json:"items"`
	}
}

func items[T any](xs []T) *ItemsOutput[T] {
	out := &ItemsOutput[T]{}
	out.Body.Items = xs
	if out.Body.Items == nil {
		out.Body.Items = []T{}
	}
	return out
}

func mapSlice[A, B any](xs []A, f func(A) B) []B {
	out := make([]B, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

type PostMessageInput struct {
	ID   int `path:"id"`
	Body struct {
		Content string `json:"content" minLength:"1" maxLength:"8000"`
	}
}

type MessageOutput struct{ Body Message }

type ProposeActionInput struct {
	ID   int `path:"id"`
	Body struct {
		Type      string         `json:"type"`
		Params    map[string]any `json:"params,omitempty"`
		Rationale string         `json:"rationale,omitempty"`
	}
}

type ActionOutput struct{ Body Action }

type ResolveInput struct {
	ID   int `path:"id"`
	Body *struct {
		Reason string `json:"reason,omitempty"`
	}
}

type ReinvestigateInput struct {
	ID   int `path:"id"`
	Body *struct {
		Deep bool `json:"deep,omitempty" doc:"Use agent.deep_model (e.g. Sonnet) instead of the default model"`
	}
}

type MuteIncidentInput struct {
	ID   int `path:"id"`
	Body struct {
		Duration string `json:"duration" example:"1h" doc:"Go duration or Nd"`
		Reason   string `json:"reason,omitempty"`
	}
}

type MuteOutput struct{ Body Mute }

type RejectInput struct {
	ID   int `path:"id"`
	Body *struct {
		Reason string `json:"reason,omitempty"`
	}
}

func (s *Server) incidentOut(ctx context.Context, inc *ent.Incident) (*IncidentOutput, error) {
	counts, err := s.deps.Incidents.Counts(ctx, []int{inc.ID})
	if err != nil {
		return nil, apiErr(err)
	}
	return &IncidentOutput{Body: toIncident(s.deps.Incidents, inc, counts[inc.ID])}, nil
}

func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		d, err := time.ParseDuration(strings.TrimSuffix(s, "d") + "h")
		return d * 24, err
	}
	return time.ParseDuration(s)
}

func (s *Server) registerIncidents() {
	svc := s.deps.Incidents
	if svc == nil {
		return
	}
	tag := "Incidents"
	huma.Register(s.api, op(http.MethodGet, "/incidents", "list-incidents", "List incidents", domain.RoleViewer, tag),
		func(ctx context.Context, in *ListIncidentsInput) (*ListIncidentsOutput, error) {
			f := in.filter()
			f.Limit, f.Cursor = in.Limit, in.Cursor
			list, total, next, err := svc.List(ctx, f)
			if err != nil {
				return nil, apiErr(err)
			}
			ids := mapSlice(list, func(i *ent.Incident) int { return i.ID })
			counts, err := svc.Counts(ctx, ids)
			if err != nil {
				return nil, apiErr(err)
			}
			out := &ListIncidentsOutput{}
			out.Body.Items = mapSlice(list, func(i *ent.Incident) Incident { return toIncident(svc, i, counts[i.ID]) })
			out.Body.Total = total
			if next > 0 {
				out.Body.NextCursor = &next
			}
			return out, nil
		})

	huma.Register(s.api, op(http.MethodGet, "/incidents/histogram", "incidents-histogram", "Incidents opened over time by severity", domain.RoleViewer, tag),
		func(ctx context.Context, in *HistogramInput) (*HistogramOutput, error) {
			until := svc.Now()
			if !in.Until.IsZero() {
				until = in.Until
			}
			since := until.Add(-24 * time.Hour)
			if !in.Since.IsZero() {
				since = in.Since
			}
			f := in.filter()
			f.Since, f.Until = nil, nil
			bs, err := svc.Histogram(ctx, f, since, until, in.Buckets)
			if err != nil {
				return nil, apiErr(err)
			}
			out := &HistogramOutput{}
			out.Body.Buckets = mapSlice(bs, func(b incident.Bucket) HistogramBucket {
				return HistogramBucket{Start: b.Start, End: b.End, Critical: b.Critical, Warning: b.Warning, Info: b.Info, Other: b.Other}
			})
			return out, nil
		})

	huma.Register(s.api, op(http.MethodGet, "/incidents/{id}", "get-incident", "Get an incident", domain.RoleViewer, tag),
		func(ctx context.Context, in *IDInput) (*IncidentDetailOutput, error) {
			inc, err := svc.Get(ctx, in.ID)
			if err != nil {
				return nil, apiErr(err)
			}
			base, err := s.incidentOut(ctx, inc)
			if err != nil {
				return nil, err
			}
			out := &IncidentDetailOutput{Body: IncidentDetail{Incident: base.Body}}
			if c, err := svc.Cost(ctx, inc.ID, time.Time{}); err == nil {
				out.Body.Cost = toCost(c)
			}
			if inv, err := svc.LatestInvestigation(ctx, inc.ID); err == nil && inv != nil {
				i := toInvestigation(inv)
				out.Body.LatestInvestigation = &i
			}
			return out, nil
		})

	huma.Register(s.api, op(http.MethodGet, "/incidents/{id}/timeline", "get-incident-timeline", "Unified incident timeline", domain.RoleViewer, tag),
		func(ctx context.Context, in *TimelineInput) (*ItemsOutput[TimelineEvent], error) {
			evs, err := svc.Timeline(ctx, in.ID, in.AfterID, 0)
			if err != nil {
				return nil, apiErr(err)
			}
			return items(mapSlice(evs, toEvent)), nil
		})

	huma.Register(s.api, op(http.MethodGet, "/incidents/{id}/signals", "get-incident-signals", "Signals of an incident", domain.RoleViewer, tag),
		func(ctx context.Context, in *IDInput) (*ItemsOutput[Signal], error) {
			sigs, err := svc.Signals(ctx, in.ID)
			if err != nil {
				return nil, apiErr(err)
			}
			return items(mapSlice(sigs, toSignal)), nil
		})

	huma.Register(s.api, op(http.MethodGet, "/incidents/{id}/investigations", "get-incident-investigations", "AI investigations (newest first)", domain.RoleViewer, tag),
		func(ctx context.Context, in *IDInput) (*ItemsOutput[Investigation], error) {
			invs, err := svc.Investigations(ctx, in.ID)
			if err != nil {
				return nil, apiErr(err)
			}
			return items(mapSlice(invs, toInvestigation)), nil
		})

	huma.Register(s.api, op(http.MethodGet, "/incidents/{id}/messages", "get-incident-messages", "Chat messages", domain.RoleViewer, tag),
		func(ctx context.Context, in *IDInput) (*ItemsOutput[Message], error) {
			msgs, err := svc.Messages(ctx, in.ID)
			if err != nil {
				return nil, apiErr(err)
			}
			return items(mapSlice(msgs, toMessage)), nil
		})

	huma.Register(s.api, op(http.MethodPost, "/incidents/{id}/messages", "post-incident-message", "Chat with the agent about the incident", domain.RoleOperator, tag),
		func(ctx context.Context, in *PostMessageInput) (*MessageOutput, error) {
			m, err := svc.PostMessage(ctx, actor(ctx), in.ID, in.Body.Content)
			if err != nil {
				return nil, apiErr(err)
			}
			return &MessageOutput{Body: toMessage(m)}, nil
		})

	huma.Register(s.api, op(http.MethodGet, "/incidents/{id}/actions", "get-incident-actions", "Actions of an incident", domain.RoleViewer, "Actions"),
		func(ctx context.Context, in *IDInput) (*ItemsOutput[Action], error) {
			as, err := svc.Actions(ctx, in.ID)
			if err != nil {
				return nil, apiErr(err)
			}
			return items(mapSlice(as, toAction)), nil
		})

	huma.Register(s.api, op(http.MethodPost, "/incidents/{id}/actions", "propose-action", "Propose an action (needs approval)", domain.RoleOperator, "Actions"),
		func(ctx context.Context, in *ProposeActionInput) (*ActionOutput, error) {
			a, err := svc.ProposeAction(ctx, actor(ctx), in.ID, in.Body.Type, in.Body.Params, in.Body.Rationale)
			if err != nil {
				return nil, apiErr(err)
			}
			return &ActionOutput{Body: toAction(a)}, nil
		})

	mutate := func(path, id, summary string, fn func(ctx context.Context, a domain.Actor, id int) (*ent.Incident, error)) {
		huma.Register(s.api, op(http.MethodPost, "/incidents/{id}/"+path, id, summary, domain.RoleOperator, tag),
			func(ctx context.Context, in *IDInput) (*IncidentOutput, error) {
				inc, err := fn(ctx, actor(ctx), in.ID)
				if err != nil {
					return nil, apiErr(err)
				}
				return s.incidentOut(ctx, inc)
			})
	}
	mutate("ack", "ack-incident", "Acknowledge (assigns to you)", svc.Ack)
	mutate("reopen", "reopen-incident", "Reopen a resolved incident", svc.Reopen)
	huma.Register(s.api, op(http.MethodPost, "/incidents/{id}/reinvestigate", "reinvestigate-incident", "Run a new AI investigation (deep: use the deeper, more expensive model)", domain.RoleOperator, tag),
		func(ctx context.Context, in *ReinvestigateInput) (*IncidentOutput, error) {
			deep := in.Body != nil && in.Body.Deep
			inc, err := svc.ReinvestigateWith(ctx, actor(ctx), in.ID, deep)
			if err != nil {
				return nil, apiErr(err)
			}
			return s.incidentOut(ctx, inc)
		})

	huma.Register(s.api, op(http.MethodPost, "/incidents/{id}/resolve", "resolve-incident", "Resolve", domain.RoleOperator, tag),
		func(ctx context.Context, in *ResolveInput) (*IncidentOutput, error) {
			reason := ""
			if in.Body != nil {
				reason = in.Body.Reason
			}
			inc, err := svc.Resolve(ctx, actor(ctx), in.ID, reason)
			if err != nil {
				return nil, apiErr(err)
			}
			return s.incidentOut(ctx, inc)
		})

	huma.Register(s.api, op(http.MethodPost, "/incidents/{id}/mute", "mute-incident", "Mute the incident's grouping labels for a duration", domain.RoleOperator, tag),
		func(ctx context.Context, in *MuteIncidentInput) (*MuteOutput, error) {
			d, err := parseDuration(in.Body.Duration)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("invalid duration")
			}
			m, err := svc.MuteIncident(ctx, actor(ctx), in.ID, d, in.Body.Reason)
			if err != nil {
				return nil, apiErr(err)
			}
			return &MuteOutput{Body: toMute(svc, m)}, nil
		})
}

func (s *Server) registerActions() {
	svc := s.deps.Incidents
	if svc == nil {
		return
	}
	tag := "Actions"
	huma.Register(s.api, op(http.MethodPost, "/actions/{id}/approve", "approve-action", "Approve a proposed action", domain.RoleOperator, tag),
		func(ctx context.Context, in *IDInput) (*ActionOutput, error) {
			a, err := svc.Approve(ctx, actor(ctx), in.ID)
			if err != nil {
				return nil, apiErr(err)
			}
			return &ActionOutput{Body: toAction(a)}, nil
		})
	huma.Register(s.api, op(http.MethodPost, "/actions/{id}/reject", "reject-action", "Reject a proposed action", domain.RoleOperator, tag),
		func(ctx context.Context, in *RejectInput) (*ActionOutput, error) {
			reason := ""
			if in.Body != nil {
				reason = in.Body.Reason
			}
			a, err := svc.Reject(ctx, actor(ctx), in.ID, reason)
			if err != nil {
				return nil, apiErr(err)
			}
			return &ActionOutput{Body: toAction(a)}, nil
		})
	huma.Register(s.api, op(http.MethodGet, "/action-types", "list-action-types", "Registered action types", domain.RoleViewer, tag),
		func(ctx context.Context, _ *struct{}) (*ItemsOutput[actions.Info], error) {
			return items(svc.Registry().Types()), nil
		})
}
