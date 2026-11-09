package incident

import (
	"context"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/action"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/db/ent/investigation"
	"github.com/xonoko/infra-monitor/internal/db/ent/message"
	"github.com/xonoko/infra-monitor/internal/db/ent/mute"
	"github.com/xonoko/infra-monitor/internal/db/ent/predicate"
	"github.com/xonoko/infra-monitor/internal/db/ent/signal"
	"github.com/xonoko/infra-monitor/internal/db/ent/timelineevent"
)

func muteActiveAt(t time.Time) []predicate.Mute {
	return []predicate.Mute{mute.StartsAtLTE(t), mute.EndsAtGT(t)}
}

// ListFilter filters incidents.
type ListFilter struct {
	Status    []string
	Severity  []string
	Verdict   []string
	Cluster   string
	Namespace string
	Query     string
	Since     *time.Time
	Until     *time.Time
	Muted     *bool
	Limit     int
	Cursor    int // return incidents with id < Cursor
}

func (f ListFilter) predicates() []predicate.Incident {
	var ps []predicate.Incident
	if len(f.Status) > 0 {
		var st []incident.Status
		for _, s := range f.Status {
			st = append(st, incident.Status(s))
		}
		ps = append(ps, incident.StatusIn(st...))
	}
	if len(f.Severity) > 0 {
		ps = append(ps, incident.SeverityIn(f.Severity...))
	}
	if len(f.Verdict) > 0 {
		var vs []incident.Verdict
		for _, v := range f.Verdict {
			vs = append(vs, incident.Verdict(v))
		}
		ps = append(ps, incident.VerdictIn(vs...))
	}
	if f.Cluster != "" {
		ps = append(ps, incident.Cluster(f.Cluster))
	}
	if f.Namespace != "" {
		ps = append(ps, incident.Namespace(f.Namespace))
	}
	if f.Muted != nil {
		ps = append(ps, incident.Muted(*f.Muted))
	}
	// Active during the window: opened before Until and not resolved before Since.
	if f.Until != nil {
		ps = append(ps, incident.OpenedAtLTE(*f.Until))
	}
	if f.Since != nil {
		ps = append(ps, incident.Or(incident.ResolvedAtIsNil(), incident.ResolvedAtGTE(*f.Since)))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
		ps = append(ps, func(sel *entsql.Selector) {
			sel.Where(entsql.Or(
				entsql.ContainsFold(sel.C(incident.FieldTitle), q),
				entsql.ContainsFold(sel.C(incident.FieldSummary), q),
				entsql.P(func(b *entsql.Builder) {
					b.WriteString("CAST(").Ident(sel.C(incident.FieldLabels)).WriteString(" AS text) ILIKE ").Arg(like)
				}),
			))
		})
	}
	return ps
}

// List returns incidents (newest first), the total count and the next cursor.
func (s *Service) List(ctx context.Context, f ListFilter) ([]*ent.Incident, int, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	ps := f.predicates()
	total, err := s.ent.Incident.Query().Where(ps...).Count(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	q := s.ent.Incident.Query().Where(ps...)
	if f.Cursor > 0 {
		q.Where(incident.IDLT(f.Cursor))
	}
	items, err := q.Order(ent.Desc(incident.FieldID)).Limit(f.Limit + 1).WithAssignee().All(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	next := 0
	if len(items) > f.Limit {
		items = items[:f.Limit]
		next = items[len(items)-1].ID
	}
	return items, total, next, nil
}

// Counts are per-incident aggregates for list rows.
type Counts struct {
	Signals, Firing, PendingActions int
}

func (s *Service) Counts(ctx context.Context, ids []int) (map[int]Counts, error) {
	out := make(map[int]Counts, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Pool.Query(ctx, `SELECT incident_id, count(*), count(*) FILTER (WHERE status = 'firing') FROM signals WHERE incident_id = ANY($1) GROUP BY incident_id`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, n, f int
		if err := rows.Scan(&id, &n, &f); err != nil {
			rows.Close()
			return nil, err
		}
		c := out[id]
		c.Signals, c.Firing = n, f
		out[id] = c
	}
	rows.Close()
	rows, err = s.db.Pool.Query(ctx, `SELECT incident_id, count(*) FROM actions WHERE incident_id = ANY($1) AND status = 'proposed' GROUP BY incident_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		c := out[id]
		c.PendingActions = n
		out[id] = c
	}
	return out, rows.Err()
}

// Bucket is one histogram bucket.
type Bucket struct {
	Start, End                     time.Time
	Critical, Warning, Info, Other int
}

// Histogram counts incidents opened per time bucket by severity.
func (s *Service) Histogram(ctx context.Context, f ListFilter, since, until time.Time, buckets int) ([]Bucket, error) {
	if buckets <= 0 || buckets > 500 {
		buckets = 60
	}
	if !until.After(since) {
		until = since.Add(time.Hour)
	}
	ps := f.predicates()
	ps = append(ps, incident.OpenedAtGTE(since), incident.OpenedAtLT(until))
	var rows []struct {
		OpenedAt time.Time `json:"opened_at"`
		Severity string    `json:"severity"`
	}
	if err := s.ent.Incident.Query().Where(ps...).Limit(50000).
		Select(incident.FieldOpenedAt, incident.FieldSeverity).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	width := until.Sub(since) / time.Duration(buckets)
	out := make([]Bucket, buckets)
	for i := range out {
		out[i].Start = since.Add(time.Duration(i) * width)
		out[i].End = out[i].Start.Add(width)
	}
	for _, r := range rows {
		i := int(r.OpenedAt.Sub(since) / width)
		if i < 0 || i >= buckets {
			continue
		}
		switch strings.ToLower(r.Severity) {
		case "critical":
			out[i].Critical++
		case "warning":
			out[i].Warning++
		case "info":
			out[i].Info++
		default:
			out[i].Other++
		}
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, id int) (*ent.Incident, error) {
	inc, err := s.ent.Incident.Query().Where(incident.ID(id)).WithAssignee().Only(ctx)
	return inc, notFound(err, "incident")
}

func (s *Service) Timeline(ctx context.Context, id, afterID, limit int) ([]*ent.TimelineEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	return s.ent.TimelineEvent.Query().Where(timelineevent.IncidentID(id), timelineevent.IDGT(afterID)).
		Order(ent.Asc(timelineevent.FieldID)).Limit(limit).All(ctx)
}

func (s *Service) Signals(ctx context.Context, id int) ([]*ent.Signal, error) {
	return s.ent.Signal.Query().Where(signal.IncidentID(id)).Order(ent.Desc(signal.FieldLastSeenAt)).Limit(500).All(ctx)
}

func (s *Service) Investigations(ctx context.Context, id int) ([]*ent.Investigation, error) {
	return s.ent.Investigation.Query().Where(investigation.IncidentID(id)).Order(ent.Desc(investigation.FieldID)).Limit(50).All(ctx)
}

func (s *Service) LatestInvestigation(ctx context.Context, id int) (*ent.Investigation, error) {
	inv, err := s.ent.Investigation.Query().Where(investigation.IncidentID(id), investigation.TriggerNEQ(investigation.TriggerCorrelation)).
		Order(ent.Desc(investigation.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return inv, err
}

func (s *Service) Messages(ctx context.Context, id int) ([]*ent.Message, error) {
	return s.ent.Message.Query().Where(message.IncidentID(id)).Order(ent.Asc(message.FieldID)).Limit(1000).All(ctx)
}

func (s *Service) Actions(ctx context.Context, id int) ([]*ent.Action, error) {
	return s.ent.Action.Query().Where(action.IncidentID(id)).Order(ent.Desc(action.FieldID)).Limit(200).All(ctx)
}

func (s *Service) GetAction(ctx context.Context, id int) (*ent.Action, error) {
	a, err := s.ent.Action.Get(ctx, id)
	return a, notFound(err, "action")
}

func (s *Service) Mutes(ctx context.Context, activeOnly bool) ([]*ent.Mute, error) {
	q := s.ent.Mute.Query()
	if activeOnly {
		q.Where(mute.EndsAtGT(s.now()))
	}
	return q.Order(ent.Desc(mute.FieldID)).Limit(500).All(ctx)
}

// MuteActive reports whether a mute is active now.
func (s *Service) MuteActive(m *ent.Mute) bool {
	now := s.now()
	return !m.StartsAt.After(now) && m.EndsAt.After(now)
}

// Now exposes the service clock.
func (s *Service) Now() time.Time { return s.now() }

// Retention deletes resolved/closed incidents older than the retention window.
func (s *Service) Retention(ctx context.Context) (int, error) {
	if s.cfg.Retention.ResolvedIncidents <= 0 {
		return 0, nil
	}
	cut := s.now().Add(-s.cfg.Retention.ResolvedIncidents)
	ids, err := s.ent.Incident.Query().Where(incident.StatusIn(incident.StatusResolved, incident.StatusClosed), incident.ResolvedAtLT(cut)).Limit(1000).IDs(ctx)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if _, err := s.ent.Signal.Delete().Where(signal.IncidentIDIn(ids...)).Exec(ctx); err != nil {
		return 0, err
	}
	n, err := s.ent.Incident.Delete().Where(incident.IDIn(ids...)).Exec(ctx)
	return n, err
}

// PublicURL is the UI base URL.
func (s *Service) PublicURL() string { return s.cfg.Server.PublicURL }

// Ent exposes the ent client for read-only status queries.
func (s *Service) Ent() *ent.Client { return s.ent }
