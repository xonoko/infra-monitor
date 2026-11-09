package incident

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/action"
	"github.com/xonoko/infra-monitor/internal/db/ent/heartbeat"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/db/ent/signal"
	"github.com/xonoko/infra-monitor/internal/db/ent/source"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// IngestResult summarizes one batch.
type IngestResult struct {
	Received   int
	Heartbeats int
	Queued     int
}

// IngestSignals upserts signals by (source, fingerprint) and queues processing for every
// state transition. Heartbeat signals only refresh their heartbeat. Called from webhooks
// and reconcilers; returns quickly (processing is async).
func (s *Service) IngestSignals(ctx context.Context, sourceName string, sigs []domain.Signal) (IngestResult, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "incident.ingest")
	defer span.End()
	span.SetAttributes(attribute.String("source", sourceName), attribute.Int("signals", len(sigs)))
	res := IngestResult{Received: len(sigs)}
	src, ok := s.cfg.Source(sourceName)
	if !ok {
		return res, fmt.Errorf("%w: unknown source %q", ErrNotFound, sourceName)
	}
	now := s.now()
	err := s.tx(ctx, func(tx *ent.Tx) error {
		for _, sig := range sigs {
			sig.Source = sourceName
			if s.isHeartbeat(sig) {
				if sig.Status == "firing" {
					if err := s.touchHeartbeat(ctx, tx, sourceName, now); err != nil {
						return err
					}
				}
				res.Heartbeats++
				continue
			}
			queued, err := s.upsertSignal(ctx, tx, src, sig, now, false)
			if err != nil {
				return err
			}
			if queued {
				res.Queued++
			}
			s.m.signals.Add(ctx, 1, metric.WithAttributes(attribute.String("source", sourceName), attribute.String("status", sig.Status)))
		}
		return s.touchSource(ctx, tx, src, now, "")
	})
	if err != nil {
		telemetry.RecordError(span, err)
		_ = s.recordSourceError(context.WithoutCancel(ctx), src, err)
	}
	return res, err
}

func (s *Service) touchSource(ctx context.Context, tx *ent.Tx, src *config.SourceConfig, now time.Time, lastErr string) error {
	return tx.Source.Create().
		SetName(src.Name).SetType(src.Type).SetLastReceivedAt(now).SetReceivedTotal(1).SetLastError(lastErr).
		OnConflictColumns(source.FieldName).
		Update(func(u *ent.SourceUpsert) {
			u.SetLastReceivedAt(now)
			u.AddReceivedTotal(1)
			u.SetUpdatedAt(now)
		}).Exec(ctx)
}

func (s *Service) recordSourceError(ctx context.Context, src *config.SourceConfig, err error) error {
	now := s.now()
	return s.ent.Source.Create().SetName(src.Name).SetType(src.Type).SetLastError(err.Error()).SetLastErrorAt(now).
		OnConflictColumns(source.FieldName).
		Update(func(u *ent.SourceUpsert) { u.SetLastError(err.Error()); u.SetLastErrorAt(now) }).Exec(ctx)
}

// upsertSignal stores the signal and queues processing on transitions. Repeats (same state
// re-sent by Alertmanager) only refresh last_seen_at.
func (s *Service) upsertSignal(ctx context.Context, tx *ent.Tx, src *config.SourceConfig, sig domain.Signal, now time.Time, stale bool) (bool, error) {
	if sig.Fingerprint == "" {
		sig.Fingerprint = Fingerprint(sig.Labels)
	}
	if sig.Labels == nil {
		sig.Labels = map[string]string{}
	}
	if sig.Annotations == nil {
		sig.Annotations = map[string]string{}
	}
	if sig.StartsAt.IsZero() {
		sig.StartsAt = now
	}
	cluster := sig.Labels[src.ClusterLabel]
	if cluster == "" {
		cluster = src.DefaultCluster
	}
	namespace := sig.Labels[src.NamespaceLabel]
	existing, err := tx.Signal.Query().
		Where(signal.Source(sig.Source), signal.Fingerprint(sig.Fingerprint)).
		ForUpdate().Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return false, err
	}
	var transition string
	var id int
	if existing == nil {
		if sig.Status == "resolved" {
			transition = "resolved"
		} else {
			transition = "new"
		}
		c := tx.Signal.Create().
			SetSource(sig.Source).SetFingerprint(sig.Fingerprint).SetStatus(signal.Status(sig.Status)).
			SetName(sig.Labels["alertname"]).SetSeverity(sig.Labels["severity"]).
			SetCluster(cluster).SetNamespace(namespace).
			SetLabels(sig.Labels).SetAnnotations(sig.Annotations).
			SetStartsAt(sig.StartsAt).SetNillableEndsAt(sig.EndsAt).
			SetGeneratorURL(sig.GeneratorURL).SetLastSeenAt(now)
		if len(sig.Raw) > 0 {
			c.SetRaw(json.RawMessage(sig.Raw))
		}
		created, err := c.Save(ctx)
		if err != nil {
			return false, err
		}
		id = created.ID
	} else {
		id = existing.ID
		u := tx.Signal.UpdateOneID(existing.ID).
			SetLastSeenAt(now).SetLabels(sig.Labels).SetAnnotations(sig.Annotations).
			SetName(sig.Labels["alertname"]).SetSeverity(sig.Labels["severity"]).
			SetCluster(cluster).SetNamespace(namespace).SetGeneratorURL(sig.GeneratorURL)
		if len(sig.Raw) > 0 {
			u.SetRaw(json.RawMessage(sig.Raw))
		}
		switch {
		case existing.Status == signal.StatusFiring && sig.Status == "resolved":
			transition = "resolved"
			if stale {
				transition = "stale"
			}
			end := now
			if sig.EndsAt != nil {
				end = *sig.EndsAt
			}
			u.SetStatus(signal.StatusResolved).SetEndsAt(end)
		case existing.Status == signal.StatusResolved && sig.Status == "firing":
			transition = "refire"
			u.SetStatus(signal.StatusFiring).ClearEndsAt().SetStartsAt(sig.StartsAt).AddFireCount(1)
		case existing.Status == signal.StatusFiring && existing.IncidentID == nil:
			// never attached (e.g. processing failed): process again
			transition = "new"
		default:
			transition = "repeat"
		}
		if err := u.Exec(ctx); err != nil {
			return false, err
		}
	}
	if transition == "repeat" {
		return false, nil
	}
	return true, enqueue(ctx, tx, JobProcessSignal, processSignalPayload{SignalID: id, Transition: transition}, queue.Opts{MaxAttempts: 8})
}

// Fingerprint computes a stable fingerprint from labels (same idea as Alertmanager).
func Fingerprint(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New64a()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0xff})
		_, _ = h.Write([]byte(labels[k]))
		_, _ = h.Write([]byte{0xff})
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

func (s *Service) isHeartbeat(sig domain.Signal) bool {
	return len(s.heartbeatMatchers) > 0 && domain.MatchAll(s.heartbeatMatchers, sig.Labels)
}

// GroupingKey builds the correlation key from the configured group_by labels.
func (s *Service) GroupingKey(sig *ent.Signal) string {
	parts := make([]string, 0, len(s.cfg.Correlation.GroupBy))
	for _, l := range s.cfg.Correlation.GroupBy {
		v := sig.Labels[l]
		switch l {
		case "cluster":
			v = sig.Cluster
		case "namespace":
			v = sig.Namespace
		}
		parts = append(parts, l+"="+v)
	}
	return strings.Join(parts, "|")
}

// ProcessSignal correlates a signal into an incident. Runs as a job.
func (s *Service) ProcessSignal(ctx context.Context, signalID int, transition string) error {
	ctx, span := telemetry.Tracer().Start(ctx, "incident.process_signal")
	defer span.End()
	span.SetAttributes(attribute.Int("signal.id", signalID), attribute.String("transition", transition))
	return s.tx(ctx, func(tx *ent.Tx) error {
		sig, err := tx.Signal.Get(ctx, signalID)
		if err != nil {
			return notFoundPermanent(err)
		}
		key := s.GroupingKey(sig)
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey("group:"+key)); err != nil {
			return err
		}
		if sig.Status == signal.StatusResolved {
			return s.processResolved(ctx, tx, sig, transition)
		}
		return s.processFiring(ctx, tx, sig, key, transition)
	})
}

func notFoundPermanent(err error) error {
	if ent.IsNotFound(err) {
		return queue.Permanent(err)
	}
	return err
}

func lockKey(s string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int64(h.Sum64())
}

func signalPayload(sig *ent.Signal) map[string]any {
	return map[string]any{
		"signal_id": sig.ID, "name": sig.Name, "severity": sig.Severity, "fingerprint": sig.Fingerprint,
		"summary": firstNonEmpty(sig.Annotations["summary"], sig.Annotations["description"], sig.Annotations["message"]),
	}
}

func (s *Service) processFiring(ctx context.Context, tx *ent.Tx, sig *ent.Signal, key, transition string) error {
	now := s.now()
	// Already attached to an active incident?
	if sig.IncidentID != nil {
		inc, err := tx.Incident.Query().Where(incident.ID(*sig.IncidentID)).ForUpdate().Only(ctx)
		if err == nil && (inc.Status == incident.StatusOpen || inc.Status == incident.StatusAcknowledged) {
			if transition != "refire" {
				// duplicate processing of an already attached signal
				return tx.Incident.UpdateOne(inc).SetLastSignalAt(now).Exec(ctx)
			}
			upd := tx.Incident.UpdateOne(inc).SetLastSignalAt(now).SetSeverity(domain.MaxSeverity(inc.Severity, sig.Severity))
			if inc, err = upd.Save(ctx); err != nil {
				return err
			}
			_, err = s.addEvent(ctx, tx, inc, domain.EventSignalFiring, domain.System, signalPayload(sig))
			return err
		}
	}
	// Active incident with the same key?
	inc, err := tx.Incident.Query().
		Where(incident.GroupingKey(key), incident.StatusIn(incident.StatusOpen, incident.StatusAcknowledged)).
		Order(ent.Desc(incident.FieldID)).ForUpdate().First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if inc != nil {
		if inc, err = tx.Incident.UpdateOne(inc).SetLastSignalAt(now).SetSeverity(domain.MaxSeverity(inc.Severity, sig.Severity)).Save(ctx); err != nil {
			return err
		}
		if err := tx.Signal.UpdateOne(sig).SetIncidentID(inc.ID).Exec(ctx); err != nil {
			return err
		}
		_, err = s.addEvent(ctx, tx, inc, domain.EventSignalFiring, domain.System, signalPayload(sig))
		return err
	}
	// Recently resolved incident with the same key? Reopen instead of duplicating (flapping).
	reopenAfter := now.Add(-s.cfg.Correlation.ReopenWindow)
	inc, err = tx.Incident.Query().
		Where(incident.GroupingKey(key), incident.StatusEQ(incident.StatusResolved), incident.ResolvedAtGT(reopenAfter)).
		Order(ent.Desc(incident.FieldResolvedAt)).ForUpdate().First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if inc != nil && inc.MergedIntoID != nil {
		// Its alerts were grouped into an older incident: they go back there.
		if inc, err = s.mergedTarget(ctx, tx, inc); err != nil {
			return err
		}
		if inc.Status == incident.StatusOpen || inc.Status == incident.StatusAcknowledged {
			if inc, err = tx.Incident.UpdateOne(inc).SetLastSignalAt(now).SetSeverity(domain.MaxSeverity(inc.Severity, sig.Severity)).Save(ctx); err != nil {
				return err
			}
			if err := tx.Signal.UpdateOne(sig).SetIncidentID(inc.ID).Exec(ctx); err != nil {
				return err
			}
			_, err = s.addEvent(ctx, tx, inc, domain.EventSignalFiring, domain.System, signalPayload(sig))
			return err
		}
		if inc.Status != incident.StatusResolved || inc.ResolvedAt == nil || !inc.ResolvedAt.After(reopenAfter) {
			inc = nil // too old: a new incident
		}
	}
	if inc != nil {
		if inc, err = tx.Incident.UpdateOne(inc).
			SetStatus(incident.StatusOpen).ClearResolvedAt().SetLastSignalAt(now).AddReopenCount(1).
			SetSeverity(domain.MaxSeverity(inc.Severity, sig.Severity)).Save(ctx); err != nil {
			return err
		}
		if err := tx.Signal.UpdateOne(sig).SetIncidentID(inc.ID).Exec(ctx); err != nil {
			return err
		}
		s.m.incidents.Add(ctx, 1, metric.WithAttributes(attribute.String("severity", inc.Severity), attribute.String("kind", "reopened")))
		if _, err := s.addEvent(ctx, tx, inc, domain.EventIncidentReopened, domain.System, map[string]any{"from": "resolved", "to": "open", "reopen_count": inc.ReopenCount}); err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventSignalFiring, domain.System, signalPayload(sig)); err != nil {
			return err
		}
		return s.afterOpen(ctx, tx, inc, false)
	}
	// New incident.
	labels := map[string]string{}
	for k, v := range sig.Labels {
		labels[k] = v
	}
	title := firstNonEmpty(sig.Annotations["summary"], sig.Name, "Alert")
	if sig.Name != "" && sig.Annotations["summary"] != "" && !strings.Contains(sig.Annotations["summary"], sig.Name) {
		title = sig.Name + ": " + sig.Annotations["summary"]
	}
	inc, err = tx.Incident.Create().
		SetTitle(truncate(title, 300)).SetSeverity(sig.Severity).SetGroupingKey(key).SetSource(sig.Source).
		SetCluster(sig.Cluster).SetNamespace(sig.Namespace).SetLabels(labels).
		SetOpenedAt(now).SetLastSignalAt(now).SetTraceID(telemetry.TraceID(ctx)).
		Save(ctx)
	if err != nil {
		return err
	}
	if err := tx.Signal.UpdateOne(sig).SetIncidentID(inc.ID).Exec(ctx); err != nil {
		return err
	}
	s.m.incidents.Add(ctx, 1, metric.WithAttributes(attribute.String("severity", inc.Severity), attribute.String("kind", "opened")))
	if _, err := s.addEvent(ctx, tx, inc, domain.EventIncidentOpened, domain.System, map[string]any{"grouping_key": key}); err != nil {
		return err
	}
	if _, err := s.addEvent(ctx, tx, inc, domain.EventSignalFiring, domain.System, signalPayload(sig)); err != nil {
		return err
	}
	return s.afterOpen(ctx, tx, inc, true)
}

// afterOpen applies mutes, then schedules the debounced investigation and the fail-open notification.
func (s *Service) afterOpen(ctx context.Context, tx *ent.Tx, inc *ent.Incident, created bool) error {
	inc, err := s.applyActiveMutes(ctx, tx, inc)
	if err != nil {
		return err
	}
	debounce := s.cfg.Correlation.Debounce
	if inc.Muted {
		return nil
	}
	if s.agent == nil || inc.Internal {
		return enqueue(ctx, tx, JobFailOpen, incidentPayload{IncidentID: inc.ID}, queue.Opts{Delay: debounce, UniqueKey: fmt.Sprintf("failopen:%d", inc.ID)})
	}
	if !created && inc.LastInvestigatedAt != nil && inc.InvestigationState == incident.InvestigationStateDone {
		// Recurrence (reopened within correlation.reopen_window): reuse the existing analysis, no new AI call.
		// Only a usable one: not if the latest investigation failed or was skipped, and not if a fix ran since
		// (the problem came back despite it: the analysis is stale). A human can still ask for a new one.
		fixed, err := tx.Action.Query().Where(action.IncidentID(inc.ID), action.StatusEQ(action.StatusSucceeded),
			action.TypeNEQ(ReinvestigateType), action.FinishedAtGT(*inc.LastInvestigatedAt)).Exist(ctx)
		if err != nil {
			return err
		}
		if !fixed {
			// Same problem as another open incident in the namespace? The group check merges it, or notifies.
			return enqueue(ctx, tx, JobGroupCheck, incidentPayload{IncidentID: inc.ID}, queue.Opts{Delay: debounce, UniqueKey: fmt.Sprintf("groupcheck:%d", inc.ID)})
		}
	}
	if err := tx.Incident.UpdateOne(inc).SetInvestigationState(incident.InvestigationStatePending).Exec(ctx); err != nil {
		return err
	}
	if err := enqueue(ctx, tx, JobInvestigate, incidentPayload{IncidentID: inc.ID, Trigger: "auto"},
		queue.Opts{Delay: debounce, UniqueKey: fmt.Sprintf("investigate:%d", inc.ID), MaxAttempts: 3}); err != nil {
		return err
	}
	return enqueue(ctx, tx, JobFailOpen, incidentPayload{IncidentID: inc.ID},
		queue.Opts{Delay: debounce + s.cfg.Agent.NotifyTimeout, UniqueKey: fmt.Sprintf("failopen:%d", inc.ID)})
}

func (s *Service) processResolved(ctx context.Context, tx *ent.Tx, sig *ent.Signal, transition string) error {
	if sig.IncidentID == nil {
		return nil
	}
	inc, err := tx.Incident.Query().Where(incident.ID(*sig.IncidentID)).ForUpdate().Only(ctx)
	if err != nil {
		return notFoundPermanent(err)
	}
	p := signalPayload(sig)
	p["stale"] = transition == "stale"
	if _, err := s.addEvent(ctx, tx, inc, domain.EventSignalResolved, domain.System, p); err != nil {
		return err
	}
	if inc.Status != incident.StatusOpen && inc.Status != incident.StatusAcknowledged {
		return nil
	}
	firing, err := tx.Signal.Query().Where(signal.IncidentID(inc.ID), signal.StatusEQ(signal.StatusFiring)).Count(ctx)
	if err != nil {
		return err
	}
	if firing > 0 {
		return nil
	}
	return enqueue(ctx, tx, JobResolveIncident, incidentPayload{IncidentID: inc.ID},
		queue.Opts{Delay: s.cfg.Correlation.ResolveGrace, UniqueKey: fmt.Sprintf("resolve:%d", inc.ID)})
}

// resolveIfAllResolved resolves the incident after the grace period if nothing re-fired.
func (s *Service) resolveIfAllResolved(ctx context.Context, incidentID int) error {
	return s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := tx.Incident.Query().Where(incident.ID(incidentID)).ForUpdate().Only(ctx)
		if err != nil {
			return notFoundPermanent(err)
		}
		if inc.Status != incident.StatusOpen && inc.Status != incident.StatusAcknowledged {
			return nil
		}
		firing, err := tx.Signal.Query().Where(signal.IncidentID(inc.ID), signal.StatusEQ(signal.StatusFiring)).Count(ctx)
		if err != nil || firing > 0 {
			return err
		}
		_, err = s.setResolved(ctx, tx, inc, domain.System, "all signals resolved")
		return err
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// heartbeatKey is the heartbeat row key for a source.
func heartbeatKey(source string) string { return source }

var _ = heartbeat.FieldKey
