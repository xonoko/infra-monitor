package incident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/db/ent/investigation"
	"github.com/xonoko/infra-monitor/internal/db/ent/signal"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/pubsub"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// Progress is published on the ephemeral stream channel for live UI updates.
type Progress struct {
	IncidentID int    `json:"incident_id"`
	Kind       string `json:"kind"` // tool_call | tool_result | message | done
	Tool       string `json:"tool,omitempty"`
	Text       string `json:"text,omitempty"`
}

func (s *Service) publishProgress(ctx context.Context, p Progress) {
	if len(p.Text) > 2000 {
		p.Text = p.Text[:2000] + "…"
	}
	b, _ := json.Marshal(p)
	if len(b) > 7900 { // NOTIFY payload limit is 8000 bytes
		return
	}
	if err := pubsub.Notify(context.WithoutCancel(ctx), s.db.Pool, pubsub.ChannelStream, string(b)); err != nil {
		s.log.DebugContext(ctx, "publish progress failed", "error", err)
	}
}

func (s *Service) progressFunc(ctx context.Context, incidentID int) func(agent.Progress) {
	return func(p agent.Progress) {
		s.publishProgress(ctx, Progress{IncidentID: incidentID, Kind: p.Kind, Tool: p.Tool, Text: p.Text})
		if s.Typing != nil {
			s.Typing(ctx, incidentID)
		}
	}
}

// BuildContext assembles what the agent needs to know about an incident.
func (s *Service) BuildContext(ctx context.Context, inc *ent.Incident) (agent.IncidentContext, error) {
	sigs, err := s.ent.Signal.Query().Where(signal.IncidentID(inc.ID)).Order(ent.Desc(signal.FieldLastSeenAt)).Limit(20).All(ctx)
	if err != nil {
		return agent.IncidentContext{}, err
	}
	ic := agent.IncidentContext{
		IncidentID: inc.ID, Title: inc.Title, Status: string(inc.Status), Severity: inc.Severity,
		Cluster: inc.Cluster, Namespace: inc.Namespace, Labels: inc.Labels, URL: s.IncidentURL(inc.ID), OpenedAt: inc.OpenedAt,
	}
	for _, sg := range sigs {
		ic.Signals = append(ic.Signals, agent.SignalSummary{
			Name: sg.Name, Status: string(sg.Status), Severity: sg.Severity, Labels: sg.Labels,
			Annotations: sg.Annotations, StartsAt: sg.StartsAt, GeneratorURL: sg.GeneratorURL,
		})
	}
	if last, err := s.ent.Investigation.Query().Where(investigation.IncidentID(inc.ID), investigation.StatusEQ(investigation.StatusSucceeded)).
		Order(ent.Desc(investigation.FieldID)).First(ctx); err == nil {
		ic.PreviousAnalysis = last.Summary
	}
	for _, t := range s.registry.Types() {
		ic.ActionTypes = append(ic.ActionTypes, agent.ActionType{Type: t.Type, Description: t.Description, Risk: t.Risk, ParamsSchema: t.Schema})
	}
	return ic, nil
}

func (s *Service) runInvestigation(ctx context.Context, p incidentPayload) error {
	if s.agent == nil {
		return nil
	}
	inc, err := s.ent.Incident.Get(ctx, p.IncidentID)
	if err != nil {
		return notFoundPermanent(err)
	}
	auto := p.Trigger == "" || p.Trigger == "auto"
	if auto && inc.Status != incident.StatusOpen && inc.Status != incident.StatusAcknowledged {
		return s.skipInvestigation(ctx, inc.ID, "incident already "+string(inc.Status))
	}
	if auto && inc.Muted {
		return s.skipInvestigation(ctx, inc.ID, "incident muted")
	}
	// Global concurrency across replicas.
	running, err := s.ent.Investigation.Query().Where(investigation.StatusEQ(investigation.StatusRunning),
		investigation.StartedAtGT(s.now().Add(-s.cfg.Agent.Timeout-time.Minute))).Count(ctx)
	if err != nil {
		return err
	}
	if running >= max(1, s.cfg.Agent.MaxConcurrent) {
		return queue.Snooze(15 * time.Second)
	}
	if auto && s.cfg.Agent.MaxPerHour > 0 {
		n, err := s.ent.Investigation.Query().Where(investigation.TriggerEQ(investigation.TriggerAuto),
			investigation.CreatedAtGT(s.now().Add(-time.Hour))).Count(ctx)
		if err != nil {
			return err
		}
		if n >= s.cfg.Agent.MaxPerHour {
			return s.skipInvestigation(ctx, inc.ID, fmt.Sprintf("hourly limit of %d investigations reached", s.cfg.Agent.MaxPerHour))
		}
	}

	trigger := investigation.TriggerAuto
	switch p.Trigger {
	case "manual":
		trigger = investigation.TriggerManual
	case "action":
		trigger = investigation.TriggerAction
	}
	var inv *ent.Investigation
	actor := domain.Agent
	actor.Name = s.agent.Name()
	err = s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, inc.ID)
		if err != nil {
			return err
		}
		c := tx.Investigation.Create().SetIncidentID(inc.ID).SetStatus(investigation.StatusRunning).SetTrigger(trigger).
			SetAgent(s.agent.Name()).SetModel(s.agent.Model()).SetStartedAt(s.now()).SetTraceID(telemetry.TraceID(ctx))
		if p.RequestedBy != 0 {
			c.SetRequestedByID(p.RequestedBy)
		}
		if inv, err = c.Save(ctx); err != nil {
			return err
		}
		if inc, err = tx.Incident.UpdateOne(inc).SetInvestigationState(incident.InvestigationStateRunning).Save(ctx); err != nil {
			return err
		}
		_, err = s.addEvent(ctx, tx, inc, domain.EventInvestigationStarted, actor, map[string]any{"investigation_id": inv.ID, "trigger": string(trigger)})
		return err
	})
	if err != nil {
		return err
	}

	ic, err := s.BuildContext(ctx, inc)
	if err != nil {
		return err
	}
	actx, cancel := context.WithTimeout(ctx, s.cfg.Agent.Timeout)
	defer cancel()
	actx, span := telemetry.Tracer().Start(actx, "agent.investigate")
	span.SetAttributes(attribute.Int("incident.id", inc.ID), attribute.String("agent", s.agent.Name()), attribute.String("model", s.agent.Model()))
	if s.Typing != nil {
		s.Typing(ctx, inc.ID)
	}
	res, aerr := s.agent.Investigate(actx, ic, s.progressFunc(ctx, inc.ID))
	telemetry.RecordError(span, aerr)
	span.End()
	s.publishProgress(ctx, Progress{IncidentID: inc.ID, Kind: "done"})
	if aerr != nil {
		status := investigation.StatusFailed
		if errors.Is(aerr, context.DeadlineExceeded) {
			status = investigation.StatusTimedOut
		}
		s.m.investigations.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", string(status))))
		s.log.WarnContext(ctx, "investigation failed", "incident_id", inc.ID, "error", aerr)
		return s.finishInvestigationFailed(context.WithoutCancel(ctx), inc.ID, inv.ID, status, aerr, actor)
	}
	s.m.investigations.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "succeeded")))
	return s.finishInvestigation(context.WithoutCancel(ctx), inc.ID, inv.ID, res, actor)
}

func (s *Service) skipInvestigation(ctx context.Context, id int, reason string) error {
	return s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if inc, err = tx.Incident.UpdateOne(inc).SetInvestigationState(incident.InvestigationStateSkipped).Save(ctx); err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventInvestigationSkipped, domain.System, map[string]any{"reason": reason}); err != nil {
			return err
		}
		_, err = s.decideNotification(ctx, tx, inc, "AI investigation skipped: "+reason)
		return err
	})
}

func (s *Service) finishInvestigationFailed(ctx context.Context, incID, invID int, status investigation.Status, aerr error, actor domain.Actor) error {
	return s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, incID)
		if err != nil {
			return err
		}
		if err := tx.Investigation.UpdateOneID(invID).SetStatus(status).SetError(truncate(aerr.Error(), 4000)).SetFinishedAt(s.now()).Exec(ctx); err != nil {
			return err
		}
		if inc, err = tx.Incident.UpdateOne(inc).SetInvestigationState(incident.InvestigationStateFailed).Save(ctx); err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventInvestigationFailed, actor, map[string]any{
			"investigation_id": invID, "status": string(status), "error": truncate(aerr.Error(), 500),
		}); err != nil {
			return err
		}
		_, err = s.decideNotification(ctx, tx, inc, "AI investigation unavailable (fail open)")
		return err
	})
}

func (s *Service) finishInvestigation(ctx context.Context, incID, invID int, res *agent.InvestigationResult, actor domain.Actor) error {
	verdict := normalizeVerdict(res.Verdict)
	return s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, incID)
		if err != nil {
			return err
		}
		u := tx.Investigation.UpdateOneID(invID).SetStatus(investigation.StatusSucceeded).SetFinishedAt(s.now()).
			SetAnalysis(res.Analysis).SetSummary(res.Summary).SetRootCause(res.RootCause).
			SetVerdict(investigation.Verdict(verdict)).SetConfidence(res.Confidence).
			SetPromptTokens(res.Usage.PromptTokens).SetCompletionTokens(res.Usage.CompletionTokens)
		if res.Model != "" {
			u.SetModel(res.Model)
		}
		if len(res.ToolCalls) > 0 && json.Valid(res.ToolCalls) {
			u.SetToolCalls(res.ToolCalls)
		}
		if err := u.Exec(ctx); err != nil {
			return err
		}
		iu := tx.Incident.UpdateOne(inc).SetInvestigationState(incident.InvestigationStateDone).
			SetVerdict(incident.Verdict(verdict)).SetSummary(firstNonEmpty(res.Summary, truncate(res.Analysis, 600))).
			SetLastInvestigatedAt(s.now())
		if len(res.Conversation) > 0 && json.Valid(res.Conversation) {
			iu.SetAgentConversation(res.Conversation)
		}
		if inc, err = iu.Save(ctx); err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventInvestigationCompleted, actor, map[string]any{
			"investigation_id": invID, "verdict": verdict, "confidence": res.Confidence,
			"summary": truncate(res.Summary, 1000), "root_cause": truncate(res.RootCause, 1000),
		}); err != nil {
			return err
		}
		if _, err := s.proposeFromAgent(ctx, tx, inc, res.ProposedActions, &invID, actor); err != nil {
			return err
		}
		_, err = s.decideNotification(ctx, tx, inc, "investigation completed")
		return err
	})
}

func normalizeVerdict(v string) string {
	switch v {
	case "actionable", "noise":
		return v
	default:
		return "unknown"
	}
}

// proposeFromAgent validates and stores agent proposals; invalid ones are logged on the timeline.
func (s *Service) proposeFromAgent(ctx context.Context, tx *ent.Tx, inc *ent.Incident, proposals []agent.ProposedAction, invID *int, actor domain.Actor) ([]int, error) {
	var ids []int
	for _, pa := range proposals {
		a, err := s.createAction(ctx, tx, inc, actionInput{
			Type: pa.Type, Params: pa.Params, Rationale: pa.Rationale, Risk: pa.Risk,
			ProposedBy: "agent", InvestigationID: invID,
		}, actor)
		if err == nil {
			ids = append(ids, a.ID)
			continue
		}
		if errors.Is(err, ErrInvalid) || errors.Is(err, actions.ErrUnknownType) {
			s.log.WarnContext(ctx, "agent proposed an invalid action", "incident_id", inc.ID, "type", pa.Type, "error", err)
			if _, err := s.addEvent(ctx, tx, inc, domain.EventActionRejected, domain.System, map[string]any{
				"type": pa.Type, "summary": "invalid proposal ignored", "reason": truncate(err.Error(), 500), "invalid": true,
			}); err != nil {
				return nil, err
			}
			continue
		}
		return nil, err
	}
	return ids, nil
}
