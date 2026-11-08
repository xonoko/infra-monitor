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
	"github.com/xonoko/infra-monitor/internal/db/ent/investigation"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/redact"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// JobCorrectProposals gives the agent one chance to fix proposals that failed validation or the dry-run.
const JobCorrectProposals = "correct_proposals"

type proposalFailure struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
	Error  string         `json:"error"`
}

type correctionPayload struct {
	IncidentID      int               `json:"incident_id"`
	InvestigationID *int              `json:"investigation_id,omitempty"`
	Failures        []proposalFailure `json:"failures"`
}

func (s *Service) registerCorrectionJob(q *queue.Queue) {
	q.Register(JobCorrectProposals, func(ctx context.Context, j *queue.Job) error {
		var p correctionPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return s.correctProposals(ctx, p)
	}, queue.HandlerOpts{Concurrency: 1, Timeout: s.cfg.Agent.Timeout + time.Minute})
}

func (s *Service) correctProposals(ctx context.Context, p correctionPayload) error {
	if s.agent == nil || len(p.Failures) == 0 {
		return nil
	}
	if key, reason, err := s.capReason(ctx); err != nil {
		return err
	} else if key != "" {
		s.m.skipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", key)))
		s.log.InfoContext(ctx, "proposal correction skipped", "incident_id", p.IncidentID, "reason", reason)
		return nil
	}
	inc, err := s.ent.Incident.Get(ctx, p.IncidentID)
	if err != nil {
		return notFoundPermanent(err)
	}
	ic, err := s.BuildContext(ctx, inc)
	if err != nil {
		return err
	}
	if m := firstNonEmpty(s.cfg.Agent.ChatModel, s.cfg.Agent.DeepModel); m != "" && m != s.agent.Model() {
		ic.Model = m
	}
	var b strings.Builder
	b.WriteString("Your proposed remediation was rejected before any human saw it, because it is not valid as written:\n")
	for _, f := range p.Failures {
		params, _ := json.Marshal(f.Params)
		fmt.Fprintf(&b, "- %s %s\n  error: %s\n", f.Type, params, f.Error)
	}
	b.WriteString("\nUse your tools to read the live object (exact container names, current values) and propose a corrected " +
		"action that passes a server dry-run, or no action if none is appropriate. Keep the same intent; do not change scope. " +
		"Reply with the corrected proposed_actions only.")
	cctx, cancel := context.WithTimeout(ctx, s.cfg.Agent.Timeout)
	defer cancel()
	cctx, span := telemetry.Tracer().Start(cctx, "agent.correct_proposals")
	started := s.now()
	res, aerr := s.agent.Chat(cctx, agent.ChatRequest{Incident: ic, Conversation: inc.AgentConversation, Message: b.String(), Author: "infra-monitor"}, nil)
	telemetry.RecordError(span, aerr)
	span.End()
	if aerr != nil {
		s.log.WarnContext(ctx, "proposal correction failed", "incident_id", inc.ID, "error", aerr)
		return nil
	}
	s.recordUsage(ctx, "correction", res.Model, res.Usage)
	actor := domain.Agent
	actor.Name = s.agent.Name()
	ctx = context.WithoutCancel(ctx)
	return s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, inc.ID)
		if err != nil {
			return err
		}
		ids, err := s.proposeFromAgentOpts(ctx, tx, inc, res.ProposedActions, p.InvestigationID, actor, true)
		if err != nil {
			return err
		}
		summary := fmt.Sprintf("Corrected %d invalid proposal(s): %d valid proposal(s) now", len(p.Failures), len(ids))
		return tx.Investigation.Create().SetIncidentID(inc.ID).SetStatus(investigation.StatusSucceeded).
			SetTrigger(investigation.TriggerAction).SetAgent(s.agent.Name()).SetModel(res.Model).
			SetSummary(summary).SetAnalysis(redact.Text(summary + "\n\n" + res.Reply)).
			SetPromptTokens(res.Usage.PromptTokens).SetCompletionTokens(res.Usage.CompletionTokens).
			SetCostUsd(s.costUSD(res.Model, res.Usage)).SetStartedAt(started).SetFinishedAt(s.now()).
			SetTraceID(telemetry.TraceID(ctx)).Exec(ctx)
	})
}
