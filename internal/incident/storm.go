package incident

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/db/ent/timelineevent"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// StormGroupingKey identifies the "alert storm" incident.
const StormGroupingKey = "infra-monitor:alert-storm"

// EventStormMember is written on the storm incident for each grouped incident.
const EventStormMember = "storm_member"

// IsStorm reports whether inc is an alert storm incident.
func IsStorm(inc *ent.Incident) bool { return inc.GroupingKey == StormGroupingKey }

// inStorm reports whether more than storm.threshold incidents opened within storm.window.
func (s *Service) inStorm(ctx context.Context) (bool, int, error) {
	st := s.cfg.Agent.Storm
	if !st.Enabled || st.Threshold <= 0 || st.Window <= 0 {
		return false, 0, nil
	}
	n, err := s.ent.TimelineEvent.Query().Where(
		timelineevent.TypeIn(domain.EventIncidentOpened, domain.EventIncidentReopened),
		timelineevent.CreatedAtGT(s.now().Add(-st.Window)),
		timelineevent.HasIncidentWith(incident.Internal(false), incident.GroupingKeyNEQ(StormGroupingKey)),
	).Count(ctx)
	if err != nil {
		return false, 0, err
	}
	return n > st.Threshold, n, nil
}

// joinStorm attaches an incident to the open storm incident (created if needed) instead of
// investigating it. The storm incident is investigated once and is the only one notified.
func (s *Service) joinStorm(ctx context.Context, incID, recent int) error {
	var storm *ent.Incident
	var created bool
	err := s.tx(ctx, func(tx *ent.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey("group:"+StormGroupingKey)); err != nil {
			return err
		}
		inc, err := s.lockIncident(ctx, tx, incID)
		if err != nil {
			return err
		}
		now := s.now()
		storm, err = tx.Incident.Query().Where(incident.GroupingKey(StormGroupingKey), incident.StatusIn(incident.StatusOpen, incident.StatusAcknowledged)).ForUpdate().First(ctx)
		if ent.IsNotFound(err) {
			created = true
			storm, err = tx.Incident.Create().
				SetTitle("Alert storm").SetSeverity(inc.Severity).SetGroupingKey(StormGroupingKey).SetSource("infra-monitor").
				SetLabels(map[string]string{"alertname": "InfraMonitorAlertStorm", "severity": inc.Severity}).
				SetOpenedAt(now).SetLastSignalAt(now).SetTraceID(telemetry.TraceID(ctx)).Save(ctx)
			if err != nil {
				return err
			}
			if _, err := s.addEvent(ctx, tx, storm, domain.EventIncidentOpened, domain.System, map[string]any{
				"grouping_key": StormGroupingKey, "reason": fmt.Sprintf("%d incidents opened within %s", recent, s.cfg.Agent.Storm.Window),
			}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		members, err := tx.Incident.Query().Where(incident.StormID(storm.ID)).Count(ctx)
		if err != nil {
			return err
		}
		members++
		sev := domain.MaxSeverity(storm.Severity, inc.Severity)
		labels := storm.Labels
		labels["severity"] = sev
		if storm, err = tx.Incident.UpdateOne(storm).SetSeverity(sev).SetLabels(labels).SetLastSignalAt(now).
			SetTitle(fmt.Sprintf("Alert storm: %d incidents grouped", members)).Save(ctx); err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, storm, EventStormMember, domain.System, map[string]any{
			"incident_id": inc.ID, "title": inc.Title, "severity": inc.Severity, "cluster": inc.Cluster, "namespace": inc.Namespace,
		}); err != nil {
			return err
		}
		note := fmt.Sprintf("part of alert storm #%d (investigated once)", storm.ID)
		if inc, err = tx.Incident.UpdateOne(inc).SetStormID(storm.ID).SetInvestigationState(incident.InvestigationStateSkipped).
			SetInvestigationNote(note).Save(ctx); err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventInvestigationSkipped, domain.System, map[string]any{
			"reason": note, "reason_key": SkipStorm, "storm_incident_id": storm.ID,
		}); err != nil {
			return err
		}
		if created {
			return s.afterOpen(ctx, tx, storm, true)
		}
		return nil
	})
	if err == nil {
		s.m.skipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", SkipStorm)))
		s.log.InfoContext(ctx, "incident grouped into alert storm", "incident_id", incID, "storm_id", storm.ID, "new_storm", created)
	}
	return err
}

// stormSummary lists the grouped incidents for the agent and the card.
func (s *Service) stormSummary(ctx context.Context, storm *ent.Incident) (string, []*ent.Incident, error) {
	members, err := s.ent.Incident.Query().Where(incident.StormID(storm.ID)).Order(ent.Asc(incident.FieldID)).Limit(100).All(ctx)
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d incidents grouped:\n", len(members))
	for i, m := range members {
		if i == 25 {
			fmt.Fprintf(&b, "- …and %d more\n", len(members)-25)
			break
		}
		fmt.Fprintf(&b, "- #%d [%s] %s (%s/%s, %s)\n", m.ID, m.Severity, m.Title, m.Cluster, m.Namespace, m.Status)
	}
	return b.String(), members, nil
}

// ResolveStorms resolves storm incidents whose grouped incidents are all resolved (scheduler).
func (s *Service) ResolveStorms(ctx context.Context) error {
	storms, err := s.ent.Incident.Query().Where(incident.GroupingKey(StormGroupingKey), incident.StatusIn(incident.StatusOpen, incident.StatusAcknowledged)).All(ctx)
	if err != nil {
		return err
	}
	for _, st := range storms {
		open, err := s.ent.Incident.Query().Where(incident.StormID(st.ID), incident.StatusIn(incident.StatusOpen, incident.StatusAcknowledged)).Count(ctx)
		if err != nil {
			return err
		}
		if open > 0 {
			continue
		}
		if err := s.tx(ctx, func(tx *ent.Tx) error {
			inc, err := s.lockIncident(ctx, tx, st.ID)
			if err != nil || (inc.Status != incident.StatusOpen && inc.Status != incident.StatusAcknowledged) {
				return err
			}
			_, err = s.setResolved(ctx, tx, inc, domain.System, "all grouped incidents resolved")
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

var _ = queue.Opts{}
