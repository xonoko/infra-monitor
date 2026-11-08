package incident

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/heartbeat"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// EnsureHeartbeats creates a heartbeat row per configured source (state unknown).
func (s *Service) EnsureHeartbeats(ctx context.Context) error {
	if !s.cfg.Heartbeat.Enabled {
		return nil
	}
	for _, src := range s.cfg.Sources {
		err := s.ent.Heartbeat.Create().SetKey(heartbeatKey(src.Name)).
			SetExpectedIntervalSeconds(int64(s.cfg.Heartbeat.ExpectedInterval.Seconds())).
			OnConflictColumns(heartbeat.FieldKey).
			Update(func(u *ent.HeartbeatUpsert) {
				u.SetExpectedIntervalSeconds(int64(s.cfg.Heartbeat.ExpectedInterval.Seconds()))
			}).Exec(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) touchHeartbeat(ctx context.Context, tx *ent.Tx, sourceName string, now time.Time) error {
	key := heartbeatKey(sourceName)
	if err := tx.Heartbeat.Create().SetKey(key).SetLastSeenAt(now).SetState(heartbeat.StateOk).
		SetExpectedIntervalSeconds(int64(s.cfg.Heartbeat.ExpectedInterval.Seconds())).
		OnConflictColumns(heartbeat.FieldKey).
		Update(func(u *ent.HeartbeatUpsert) { u.SetLastSeenAt(now); u.SetUpdatedAt(now) }).
		Exec(ctx); err != nil {
		return err
	}
	hb, err := tx.Heartbeat.Query().Where(heartbeat.Key(key)).ForUpdate().Only(ctx)
	if err != nil {
		return err
	}
	if hb.State != heartbeat.StateOk {
		upd := tx.Heartbeat.UpdateOne(hb).SetState(heartbeat.StateOk).ClearIncidentID()
		if err := upd.Exec(ctx); err != nil {
			return err
		}
		if hb.IncidentID != nil {
			inc, err := tx.Incident.Query().Where(incident.ID(*hb.IncidentID)).ForUpdate().Only(ctx)
			if err == nil && (inc.Status == incident.StatusOpen || inc.Status == incident.StatusAcknowledged) {
				if _, err := s.setResolved(ctx, tx, inc, domain.System, "heartbeat received again"); err != nil {
					return err
				}
			}
		}
		s.log.InfoContext(ctx, "heartbeat back", "source", sourceName)
	}
	if s.cfg.Heartbeat.PingURL != "" && (hb.LastPingAt == nil || now.Sub(*hb.LastPingAt) >= s.cfg.Heartbeat.PingInterval) {
		return enqueue(ctx, tx, JobHeartbeatPing, heartbeatPayload{Key: key}, queue.Opts{UniqueKey: "hb-ping:" + key, MaxAttempts: 2})
	}
	return nil
}

// pingHeartbeat calls the external dead-man's-switch URL (healthchecks.io style).
func (s *Service) pingHeartbeat(ctx context.Context, key string) error {
	url := s.cfg.Heartbeat.PingURL
	if url == "" {
		return nil
	}
	method := strings.ToUpper(s.cfg.Heartbeat.PingMethod)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, url, http.NoBody)
	if err != nil {
		return queue.Permanent(err)
	}
	req.Header.Set("User-Agent", "infra-monitor")
	resp, err := telemetry.HTTPClient(15 * time.Second).Do(req)
	pingErr := ""
	if err != nil {
		pingErr = redactURL(err.Error(), url)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			pingErr = fmt.Sprintf("ping returned HTTP %d", resp.StatusCode)
		}
	}
	now := s.now()
	if uerr := s.ent.Heartbeat.Update().Where(heartbeat.Key(key)).SetLastPingAt(now).SetLastPingError(pingErr).Exec(ctx); uerr != nil {
		return uerr
	}
	outcome := "success"
	if pingErr != "" {
		outcome = "failure"
	}
	s.m.pings.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	state := int32(1)
	if pingErr != "" {
		state = 2
	}
	prev := s.lastPingOK.Swap(state)
	switch {
	case pingErr != "":
		s.log.WarnContext(ctx, "heartbeat ping failed", "key", key, "error", pingErr)
	case prev == 0:
		s.log.InfoContext(ctx, "heartbeat ping ok (first on this replica)", "key", key)
	case prev == 2:
		s.log.InfoContext(ctx, "heartbeat ping recovered", "key", key)
	}
	return nil
}

func redactURL(msg, url string) string {
	if url == "" {
		return msg
	}
	return strings.ReplaceAll(msg, url, "<ping-url>")
}

// CheckHeartbeats marks silent heartbeats and opens an internal incident for each.
// Runs periodically on the scheduler replica.
func (s *Service) CheckHeartbeats(ctx context.Context) error {
	if !s.cfg.Heartbeat.Enabled {
		return nil
	}
	now := s.now()
	hbs, err := s.ent.Heartbeat.Query().Where(heartbeat.StateNEQ(heartbeat.StateSilent)).All(ctx)
	if err != nil {
		return err
	}
	for _, hb := range hbs {
		last := hb.CreatedAt
		if hb.LastSeenAt != nil {
			last = *hb.LastSeenAt
		}
		if now.Sub(last) < s.cfg.Heartbeat.SilentAfter {
			continue
		}
		if err := s.markSilent(ctx, hb.ID, last); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) markSilent(ctx context.Context, hbID int, last time.Time) error {
	return s.tx(ctx, func(tx *ent.Tx) error {
		hb, err := tx.Heartbeat.Query().Where(heartbeat.ID(hbID)).ForUpdate().Only(ctx)
		if err != nil || hb.State == heartbeat.StateSilent {
			return err
		}
		now := s.now()
		key := "heartbeat:" + hb.Key
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey("group:"+key)); err != nil {
			return err
		}
		inc, err := tx.Incident.Query().Where(incident.GroupingKey(key), incident.StatusIn(incident.StatusOpen, incident.StatusAcknowledged)).First(ctx)
		if ent.IsNotFound(err) {
			title := fmt.Sprintf("Source %s silent: no heartbeat since %s", hb.Key, last.UTC().Format(time.RFC3339))
			inc, err = tx.Incident.Create().
				SetTitle(title).SetSeverity("critical").SetGroupingKey(key).SetSource(hb.Key).SetInternal(true).
				SetVerdict(incident.VerdictActionable).
				SetSummary(fmt.Sprintf("infra-monitor has not received the heartbeat alert (%s) from source %q for more than %s. "+
					"The alerting pipeline (vmalert / Alertmanager) may be down, or the webhook route is broken.",
					strings.Join(s.cfg.Heartbeat.Matchers, ", "), hb.Key, s.cfg.Heartbeat.SilentAfter)).
				SetLabels(map[string]string{"alertname": "InfraMonitorSourceSilent", "source": hb.Key, "severity": "critical"}).
				SetOpenedAt(now).SetLastSignalAt(now).SetTraceID(telemetry.TraceID(ctx)).
				Save(ctx)
			if err != nil {
				return err
			}
			if _, err := s.addEvent(ctx, tx, inc, domain.EventIncidentOpened, domain.System, map[string]any{"grouping_key": key, "heartbeat": hb.Key}); err != nil {
				return err
			}
			if err := s.afterOpen(ctx, tx, inc, true); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		s.log.WarnContext(ctx, "heartbeat silent", "source", hb.Key, "last_seen", last)
		return tx.Heartbeat.UpdateOne(hb).SetState(heartbeat.StateSilent).SetIncidentID(inc.ID).Exec(ctx)
	})
}
