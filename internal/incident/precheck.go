package incident

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/action"
	"github.com/xonoko/infra-monitor/internal/db/ent/investigation"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/redact"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// preExecutionCheck asks the agent (agent.check_model, default the deep model), right before ANY approved
// action runs (human or auto-approved), whether it is still needed AND correct and safe as proposed. If not,
// the action is canceled (expired) with the agent's reason and nothing runs.
// If the check can't run (no agent, caps, budget, error), the approved action proceeds and the timeline says so.
func (s *Service) preExecutionCheck(ctx context.Context, actionID int) (bool, error) {
	checker, ok := s.agent.(agent.ActionChecker)
	if s.agent == nil || !ok {
		return false, nil
	}
	a, err := s.ent.Action.Get(ctx, actionID)
	if err != nil || a.Status != action.StatusApproved {
		return false, nil
	}
	inc, err := s.ent.Incident.Get(ctx, a.IncidentID)
	if err != nil {
		return false, nil
	}
	note := func(text string) error {
		return s.tx(ctx, func(tx *ent.Tx) error {
			inc, err := s.lockIncident(ctx, tx, inc.ID)
			if err != nil {
				return err
			}
			_, err = s.addEvent(ctx, tx, inc, domain.EventActionChecked, domain.Agent, actionEventPayload(a, map[string]any{"precheck": text}))
			return err
		})
	}
	if key, reason, err := s.capReason(ctx); err != nil {
		return false, err
	} else if key != "" {
		s.m.skipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", key)))
		return false, note("pre-execution check skipped (" + reason + "); running the approved action")
	}
	ic, err := s.BuildContext(ctx, inc)
	if err != nil {
		return false, err
	}
	if m := firstNonEmpty(s.cfg.Agent.CheckModel, s.cfg.Agent.DeepModel); m != "" && m != s.agent.Model() {
		ic.Model = m // the final check uses the stronger model
	}
	cctx, cancel := context.WithTimeout(ctx, min(s.cfg.Agent.Timeout, 3*time.Minute))
	defer cancel()
	cctx, span := telemetry.Tracer().Start(cctx, "agent.check_action")
	span.SetAttributes(attribute.Int("action.id", a.ID), attribute.String("action.type", a.Type))
	started := s.now()
	res, cerr := checker.CheckAction(cctx, ic, describeAction(a))
	telemetry.RecordError(span, cerr)
	span.End()
	if cerr != nil {
		s.log.WarnContext(ctx, "pre-execution check failed, running the approved action", "action_id", a.ID, "error", cerr)
		return false, note("pre-execution check unavailable (" + truncate(redact.Text(cerr.Error()), 200) + "); running the approved action")
	}
	s.recordUsage(ctx, "action_check", res.Model, res.Usage)
	verdict := "go"
	if !res.StillNeeded {
		verdict = "no-go"
	}
	summary := fmt.Sprintf("Pre-execution check for action #%d: %s. %s", a.ID, verdict, redact.Text(res.Reason))
	now := s.now()
	var canceled bool
	err = s.tx(ctx, func(tx *ent.Tx) error {
		// The check is agent work on a remediation: recorded (and costed) as an action-triggered investigation.
		if err := tx.Investigation.Create().SetIncidentID(inc.ID).SetStatus(investigation.StatusSucceeded).
			SetTrigger(investigation.TriggerAction).SetAgent(s.agent.Name()).SetModel(res.Model).
			SetSummary(truncate(summary, 1000)).SetAnalysis(summary).
			SetPromptTokens(res.Usage.PromptTokens).SetCompletionTokens(res.Usage.CompletionTokens).
			SetCostUsd(s.costUSD(res.Model, res.Usage)).SetStartedAt(started).SetFinishedAt(now).
			SetTraceID(telemetry.TraceID(ctx)).Exec(ctx); err != nil {
			return err
		}
		if res.StillNeeded {
			return nil
		}
		a, inc, err := s.lockAction(ctx, tx, a.ID)
		if err != nil || a.Status != action.StatusApproved {
			return err
		}
		canceled = true
		return s.expireAction(ctx, tx, inc, a, "canceled before running: "+truncate(redact.Text(res.Reason), 500))
	})
	if err == nil && !canceled {
		err = note("pre-execution check: " + truncate(redact.Text(res.Reason), 300))
	}
	return canceled, err
}

// describeAction is the precise description the agent re-checks.
func describeAction(a *ent.Action) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Action #%d: %s\nSummary: %s\nProposed: %s (approved by %s via %s)\n", a.ID, a.Type, a.Summary,
		a.CreatedAt.UTC().Format(time.RFC3339), a.DecidedByName, a.DecidedVia)
	if a.Rationale != "" {
		fmt.Fprintf(&b, "Why it was proposed: %s\n", a.Rationale)
	}
	if p := PreparedOf(a); p != nil {
		fmt.Fprintf(&b, "Target: %s\nOperation: %s\nExpected effect: %s\nPayload:\n%s\n", p.Target, p.Operation, p.Effect, truncate(p.Payload, 3000))
	} else if params, err := json.Marshal(a.Params); err == nil {
		fmt.Fprintf(&b, "Params: %s\n", params)
	}
	return b.String()
}
