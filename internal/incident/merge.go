package incident

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/db/ent/investigation"
	"github.com/xonoko/infra-monitor/internal/db/ent/signal"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/redact"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

const maxMergeCandidates = 5

// tryMerge asks the agent (one cheap call, no tools) whether a new incident is the same problem as an older
// open incident in the same cluster and namespace. If so, its signals move there, it is closed as merged
// and never investigated or notified on its own. Fail open: any doubt or error keeps it separate.
func (s *Service) tryMerge(ctx context.Context, inc *ent.Incident) (bool, error) {
	corr, ok := s.agent.(agent.Correlator)
	if !ok || inc.Namespace == "" || inc.Internal || inc.ReopenCount > 0 || inc.MergedIntoID != nil || IsStorm(inc) {
		return false, nil
	}
	cands, err := s.ent.Incident.Query().Where(
		incident.Cluster(inc.Cluster), incident.Namespace(inc.Namespace), incident.IDLT(inc.ID),
		incident.StatusIn(incident.StatusOpen, incident.StatusAcknowledged),
		incident.Internal(false), incident.StormIDIsNil(), incident.MergedIntoIDIsNil(),
	).Order(ent.Desc(incident.FieldID)).Limit(maxMergeCandidates + 5).All(ctx)
	if err != nil {
		return false, err
	}
	var req agent.CorrelationRequest
	byID := map[int]*ent.Incident{}
	for _, c := range cands {
		if IsStorm(c) || len(req.Candidates) >= maxMergeCandidates {
			continue
		}
		cc := agent.CorrelationCandidate{IncidentID: c.ID, Title: c.Title, OpenedAt: c.OpenedAt, Summary: c.Summary}
		if inv, err := s.ent.Investigation.Query().Where(investigation.IncidentID(c.ID), investigation.StatusEQ(investigation.StatusSucceeded),
			investigation.TriggerIn(investigation.TriggerAuto, investigation.TriggerManual)).Order(ent.Desc(investigation.FieldID)).First(ctx); err == nil {
			cc.RootCause = inv.RootCause
		}
		sigs, err := s.ent.Signal.Query().Where(signal.IncidentID(c.ID)).Order(ent.Desc(signal.FieldLastSeenAt)).Limit(5).All(ctx)
		if err != nil {
			return false, err
		}
		for _, sg := range sigs {
			cc.Signals = append(cc.Signals, agent.SignalSummary{Name: sg.Name, Status: string(sg.Status), Severity: sg.Severity,
				Labels: sg.Labels, Annotations: sg.Annotations, StartsAt: sg.StartsAt})
		}
		req.Candidates = append(req.Candidates, cc)
		byID[c.ID] = c
	}
	if len(req.Candidates) == 0 {
		return false, nil
	}
	if key, reason, err := s.capReason(ctx); err != nil || key != "" {
		if key != "" {
			s.log.InfoContext(ctx, "alert grouping skipped", "incident_id", inc.ID, "reason", reason)
		}
		return false, err
	}
	if req.Alert, err = s.BuildContext(ctx, inc); err != nil {
		return false, err
	}
	req.Alert.ActionTypes, req.Alert.GitOps, req.Alert.PreviousAnalysis = nil, "", ""

	cctx, cancel := context.WithTimeout(ctx, min(s.cfg.Agent.Timeout, time.Minute))
	defer cancel()
	cctx, span := telemetry.Tracer().Start(cctx, "agent.correlate")
	span.SetAttributes(attribute.Int("incident.id", inc.ID), attribute.Int("candidates", len(req.Candidates)))
	started := s.now()
	res, cerr := corr.Correlate(cctx, req)
	telemetry.RecordError(span, cerr)
	span.End()
	if cerr != nil {
		s.log.WarnContext(ctx, "alert grouping failed, keeping the incident separate", "incident_id", inc.ID, "error", cerr)
		return false, nil
	}
	s.recordUsage(ctx, "correlation", res.Model, res.Usage)
	target := byID[res.SameAs]
	costOn := inc.ID
	if target != nil {
		costOn = target.ID
	}
	verdict := "different problem"
	if target != nil {
		verdict = fmt.Sprintf("same problem as #%d", target.ID)
	}
	summary := fmt.Sprintf("Grouping check for %s (#%d): %s. %s", inc.Title, inc.ID, verdict, redact.Text(res.Reason))
	if err := s.ent.Investigation.Create().SetIncidentID(costOn).SetStatus(investigation.StatusSucceeded).
		SetTrigger(investigation.TriggerCorrelation).SetAgent(s.agent.Name()).SetModel(res.Model).
		SetSummary(truncate(summary, 1000)).SetAnalysis(summary).
		SetPromptTokens(res.Usage.PromptTokens).SetCompletionTokens(res.Usage.CompletionTokens).
		SetCostUsd(s.costUSD(res.Model, res.Usage)).SetStartedAt(started).SetFinishedAt(s.now()).
		SetTraceID(telemetry.TraceID(ctx)).Exec(ctx); err != nil {
		return false, err
	}
	if target == nil {
		return false, nil
	}
	merged, err := s.merge(ctx, inc.ID, target.ID, redact.Text(res.Reason))
	if merged {
		s.m.skipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "merged")))
	}
	return merged, err
}

// merge moves the signals of incident src into the older incident dst and closes src as merged.
func (s *Service) merge(ctx context.Context, srcID, dstID int, reason string) (bool, error) {
	var merged bool
	err := s.tx(ctx, func(tx *ent.Tx) error {
		dst, err := s.lockIncident(ctx, tx, dstID) // older first: consistent lock order
		if err != nil {
			return err
		}
		src, err := s.lockIncident(ctx, tx, srcID)
		if err != nil {
			return err
		}
		active := func(i *ent.Incident) bool {
			return i.Status == incident.StatusOpen || i.Status == incident.StatusAcknowledged
		}
		if !active(dst) || !active(src) || src.MergedIntoID != nil || dst.MergedIntoID != nil {
			return nil // changed meanwhile: keep them separate
		}
		now := s.now()
		if err := tx.Signal.Update().Where(signal.IncidentID(src.ID)).SetIncidentID(dst.ID).Exec(ctx); err != nil {
			return err
		}
		if dst, err = tx.Incident.UpdateOne(dst).SetLastSignalAt(maxTime(dst.LastSignalAt, src.LastSignalAt)).
			SetSeverity(domain.MaxSeverity(dst.Severity, src.Severity)).Save(ctx); err != nil {
			return err
		}
		if src, err = tx.Incident.UpdateOne(src).SetStatus(incident.StatusResolved).SetResolvedAt(now).SetMergedIntoID(dst.ID).
			SetInvestigationState(incident.InvestigationStateSkipped).SetInvestigationNote(fmt.Sprintf("same problem as #%d", dst.ID)).
			Save(ctx); err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, dst, domain.EventIncidentMerged, domain.Agent, map[string]any{
			"from_incident": src.ID, "alert": src.Title, "reason": reason,
		}); err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, src, domain.EventIncidentMerged, domain.Agent, map[string]any{
			"into_incident": dst.ID, "reason": reason,
		}); err != nil {
			return err
		}
		merged = true
		return nil
	})
	return merged, err
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// mergedTarget follows merged_into pointers (a merged incident's alerts belong to its target).
func (s *Service) mergedTarget(ctx context.Context, tx *ent.Tx, inc *ent.Incident) (*ent.Incident, error) {
	for hops := 0; inc != nil && inc.MergedIntoID != nil && hops < 3; hops++ {
		next, err := tx.Incident.Query().Where(incident.ID(*inc.MergedIntoID)).ForUpdate().Only(ctx)
		if err != nil {
			return nil, err
		}
		inc = next
	}
	return inc, nil
}
