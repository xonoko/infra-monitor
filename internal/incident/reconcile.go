package incident

import (
	"context"
	"fmt"
	"time"

	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/signal"
	"github.com/xonoko/infra-monitor/internal/db/ent/source"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// ReconcileSource applies a full list of currently active alerts pulled from a source:
// missing firing signals are ingested, and firing signals absent from the list (and not seen
// for staleAfter) are resolved. Heartbeat alerts are ignored here so that a broken webhook
// route still shows up as a silent heartbeat.
func (s *Service) ReconcileSource(ctx context.Context, sourceName string, active []domain.Signal, staleAfter time.Duration) (added, resolved int, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "incident.reconcile")
	defer span.End()
	src, ok := s.cfg.Source(sourceName)
	if !ok {
		return 0, 0, fmt.Errorf("%w: unknown source %q", ErrNotFound, sourceName)
	}
	now := s.now()
	seen := map[string]bool{}
	err = s.tx(ctx, func(tx *ent.Tx) error {
		for _, sig := range active {
			sig.Source = sourceName
			if s.isHeartbeat(sig) {
				continue
			}
			if sig.Fingerprint == "" {
				sig.Fingerprint = Fingerprint(sig.Labels)
			}
			seen[sig.Fingerprint] = true
			queued, err := s.upsertSignal(ctx, tx, src, sig, now, false)
			if err != nil {
				return err
			}
			if queued {
				added++
			}
		}
		firing, err := tx.Signal.Query().Where(signal.Source(sourceName), signal.StatusEQ(signal.StatusFiring),
			signal.LastSeenAtLT(now.Add(-staleAfter))).All(ctx)
		if err != nil {
			return err
		}
		for _, f := range firing {
			if seen[f.Fingerprint] {
				continue
			}
			end := now
			if _, err := s.upsertSignal(ctx, tx, src, domain.Signal{Source: sourceName, Fingerprint: f.Fingerprint, Status: "resolved",
				Labels: f.Labels, Annotations: f.Annotations, StartsAt: f.StartsAt, EndsAt: &end, GeneratorURL: f.GeneratorURL}, now, true); err != nil {
				return err
			}
			resolved++
		}
		return tx.Source.Create().SetName(src.Name).SetType(src.Type).SetLastReconciledAt(now).
			OnConflictColumns(source.FieldName).
			Update(func(u *ent.SourceUpsert) { u.SetLastReconciledAt(now) }).Exec(ctx)
	})
	return added, resolved, err
}
