package incident

import (
	"context"
	"fmt"
	"time"

	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/action"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
)

func (s *Service) lockIncident(ctx context.Context, tx *ent.Tx, id int) (*ent.Incident, error) {
	inc, err := tx.Incident.Query().Where(incident.ID(id)).ForUpdate().Only(ctx)
	return inc, notFound(err, fmt.Sprintf("incident %d", id))
}

func (s *Service) statusEvent(ctx context.Context, tx *ent.Tx, inc *ent.Incident, from incident.Status, actor domain.Actor, reason string) error {
	_, err := s.addEvent(ctx, tx, inc, domain.EventStatusChanged, actor, map[string]any{"from": string(from), "to": string(inc.Status), "reason": reason})
	return err
}

// setResolved resolves an incident and expires its pending proposals.
func (s *Service) setResolved(ctx context.Context, tx *ent.Tx, inc *ent.Incident, actor domain.Actor, reason string) (*ent.Incident, error) {
	from := inc.Status
	now := s.now()
	inc, err := tx.Incident.UpdateOne(inc).SetStatus(incident.StatusResolved).SetResolvedAt(now).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.statusEvent(ctx, tx, inc, from, actor, reason); err != nil {
		return nil, err
	}
	pending, err := tx.Action.Query().Where(action.IncidentID(inc.ID), action.StatusEQ(action.StatusProposed)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range pending {
		if err := s.expireAction(ctx, tx, inc, a, "incident resolved"); err != nil {
			return nil, err
		}
	}
	return inc, nil
}

// Ack acknowledges an incident and assigns it to the actor.
func (s *Service) Ack(ctx context.Context, actor domain.Actor, id int) (*ent.Incident, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	var out *ent.Incident
	err := s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if inc.Status != incident.StatusOpen {
			return fmt.Errorf("%w: incident is %s", ErrConflict, inc.Status)
		}
		upd := tx.Incident.UpdateOne(inc).SetStatus(incident.StatusAcknowledged).SetAcknowledgedAt(s.now())
		if actor.UserID != 0 {
			upd.SetAssigneeID(actor.UserID)
		}
		if out, err = upd.Save(ctx); err != nil {
			return err
		}
		return s.statusEvent(ctx, tx, out, incident.StatusOpen, actor, "acknowledged")
	})
	return out, err
}

// Resolve resolves an incident manually.
func (s *Service) Resolve(ctx context.Context, actor domain.Actor, id int, reason string) (*ent.Incident, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	if reason == "" {
		reason = "resolved manually"
	}
	var out *ent.Incident
	err := s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if inc.Status == incident.StatusResolved || inc.Status == incident.StatusClosed {
			return fmt.Errorf("%w: incident is already %s", ErrConflict, inc.Status)
		}
		out, err = s.setResolved(ctx, tx, inc, actor, reason)
		return err
	})
	return out, err
}

// Reopen reopens a resolved incident.
func (s *Service) Reopen(ctx context.Context, actor domain.Actor, id int) (*ent.Incident, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	var out *ent.Incident
	err := s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if inc.Status != incident.StatusResolved && inc.Status != incident.StatusClosed {
			return fmt.Errorf("%w: incident is %s", ErrConflict, inc.Status)
		}
		from := inc.Status
		if out, err = tx.Incident.UpdateOne(inc).SetStatus(incident.StatusOpen).ClearResolvedAt().AddReopenCount(1).Save(ctx); err != nil {
			return err
		}
		return s.statusEvent(ctx, tx, out, from, actor, "reopened manually")
	})
	return out, err
}

// Reinvestigate queues a new investigation now with the default model.
func (s *Service) Reinvestigate(ctx context.Context, actor domain.Actor, id int) (*ent.Incident, error) {
	return s.ReinvestigateWith(ctx, actor, id, false)
}

// ReinvestigateWith queues a manual investigation; deep uses agent.deep_model (explicit request only).
// Hard caps and the budget pause still apply when the job runs.
func (s *Service) ReinvestigateWith(ctx context.Context, actor domain.Actor, id int, deep bool) (*ent.Incident, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	if s.agent == nil {
		return nil, fmt.Errorf("%w: no agent configured", ErrConflict)
	}
	var out *ent.Incident
	err := s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if inc.InvestigationState == incident.InvestigationStateRunning {
			return fmt.Errorf("%w: an investigation is already running", ErrConflict)
		}
		if out, err = tx.Incident.UpdateOne(inc).SetInvestigationState(incident.InvestigationStatePending).Save(ctx); err != nil {
			return err
		}
		return enqueue(ctx, tx, JobInvestigate, incidentPayload{IncidentID: id, Trigger: "manual", RequestedBy: actor.UserID, Deep: deep},
			queue.Opts{UniqueKey: fmt.Sprintf("investigate:%d", id), MaxAttempts: 3})
	})
	return out, err
}

// ---- mutes ----

// CreateMuteInput describes a new mute.
type CreateMuteInput struct {
	Matchers   []domain.Matcher
	StartsAt   time.Time
	EndsAt     time.Time
	Reason     string
	IncidentID *int
	// MirrorTo mirrors the mute as an Alertmanager silence on that source.
	MirrorTo string
}

// CreateMute stores a mute and marks matching active incidents as muted.
func (s *Service) CreateMute(ctx context.Context, actor domain.Actor, in CreateMuteInput) (*ent.Mute, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	if len(in.Matchers) == 0 {
		return nil, fmt.Errorf("%w: at least one matcher is required", ErrInvalid)
	}
	for i := range in.Matchers {
		if err := in.Matchers[i].Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	now := s.now()
	if in.StartsAt.IsZero() {
		in.StartsAt = now
	}
	if !in.EndsAt.After(in.StartsAt) || !in.EndsAt.After(now) {
		return nil, fmt.Errorf("%w: ends_at must be in the future and after starts_at", ErrInvalid)
	}
	silenceID := ""
	if in.MirrorTo != "" {
		sil, ok := s.silencers[in.MirrorTo]
		if !ok {
			return nil, fmt.Errorf("%w: source %q has no alertmanager_url for silences", ErrInvalid, in.MirrorTo)
		}
		id, err := sil.CreateSilence(ctx, in.Matchers, in.StartsAt, in.EndsAt, actor.Name, "infra-monitor: "+in.Reason)
		if err != nil {
			return nil, fmt.Errorf("create alertmanager silence: %w", err)
		}
		silenceID = id
	}
	var out *ent.Mute
	err := s.tx(ctx, func(tx *ent.Tx) error {
		var err error
		out, err = tx.Mute.Create().
			SetMatchers(in.Matchers).SetStartsAt(in.StartsAt).SetEndsAt(in.EndsAt).SetReason(in.Reason).
			SetNillableCreatedByID(actor.UserIDPtr()).SetCreatedByName(actor.Name).
			SetNillableIncidentID(in.IncidentID).SetAlertmanagerSilenceID(silenceID).SetAlertmanagerSource(in.MirrorTo).
			Save(ctx)
		if err != nil {
			return err
		}
		if in.StartsAt.After(now) {
			return nil
		}
		active, err := tx.Incident.Query().Where(incident.StatusIn(incident.StatusOpen, incident.StatusAcknowledged), incident.Muted(false)).ForUpdate().All(ctx)
		if err != nil {
			return err
		}
		for _, inc := range active {
			if !domain.MatchAll(out.Matchers, incidentMatchLabels(inc)) && (in.IncidentID == nil || *in.IncidentID != inc.ID) {
				continue
			}
			inc, err = tx.Incident.UpdateOne(inc).SetMuted(true).SetMuteID(out.ID).Save(ctx)
			if err != nil {
				return err
			}
			if _, err := s.addEvent(ctx, tx, inc, domain.EventMuteCreated, actor, map[string]any{
				"mute_id": out.ID, "ends_at": out.EndsAt, "reason": out.Reason, "matchers": matchersString(out.Matchers),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// MuteIncident mutes an incident (and its future re-fires) by its grouping labels for d.
func (s *Service) MuteIncident(ctx context.Context, actor domain.Actor, id int, d time.Duration, reason string) (*ent.Mute, error) {
	if d <= 0 || d > 30*24*time.Hour {
		return nil, fmt.Errorf("%w: duration must be between 1m and 30d", ErrInvalid)
	}
	inc, err := s.ent.Incident.Get(ctx, id)
	if err != nil {
		return nil, notFound(err, "incident")
	}
	labels := incidentMatchLabels(inc)
	var ms []domain.Matcher
	for _, l := range s.cfg.Correlation.GroupBy {
		if v := labels[l]; v != "" {
			ms = append(ms, domain.Matcher{Name: l, Op: "=", Value: v})
		}
	}
	if len(ms) == 0 {
		ms = []domain.Matcher{{Name: "alertname", Op: "=", Value: labels["alertname"]}}
	}
	if reason == "" {
		reason = fmt.Sprintf("muted from incident #%d", id)
	}
	return s.CreateMute(ctx, actor, CreateMuteInput{Matchers: ms, EndsAt: s.now().Add(d), Reason: reason, IncidentID: &id})
}

// ExpireMute ends a mute now and unmutes incidents no other mute covers.
func (s *Service) ExpireMute(ctx context.Context, actor domain.Actor, id int) (*ent.Mute, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	var out *ent.Mute
	err := s.tx(ctx, func(tx *ent.Tx) error {
		m, err := tx.Mute.Get(ctx, id)
		if err != nil {
			return notFound(err, "mute")
		}
		now := s.now()
		if m.EndsAt.After(now) {
			if m, err = tx.Mute.UpdateOne(m).SetEndsAt(now).SetExpiredManually(true).Save(ctx); err != nil {
				return err
			}
		}
		out = m
		return s.unmuteFor(ctx, tx, m, actor)
	})
	if err == nil && out.AlertmanagerSilenceID != "" {
		if sil, ok := s.silencers[out.AlertmanagerSource]; ok {
			if serr := sil.ExpireSilence(ctx, out.AlertmanagerSilenceID); serr != nil {
				s.log.WarnContext(ctx, "expire alertmanager silence failed", "silence", out.AlertmanagerSilenceID, "error", serr)
			}
		}
	}
	return out, err
}

func (s *Service) unmuteFor(ctx context.Context, tx *ent.Tx, m *ent.Mute, actor domain.Actor) error {
	incs, err := tx.Incident.Query().Where(incident.MuteID(m.ID), incident.Muted(true)).ForUpdate().All(ctx)
	if err != nil {
		return err
	}
	for _, inc := range incs {
		if inc, err = tx.Incident.UpdateOne(inc).SetMuted(false).ClearMuteID().Save(ctx); err != nil {
			return err
		}
		if inc, err = s.applyActiveMutes(ctx, tx, inc); err != nil {
			return err
		}
		if inc.Muted {
			continue
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventMuteEnded, actor, map[string]any{"mute_id": m.ID}); err != nil {
			return err
		}
	}
	return nil
}

// EndExpiredMutes unmutes incidents whose mute ended (scheduler).
func (s *Service) EndExpiredMutes(ctx context.Context) error {
	now := s.now()
	return s.tx(ctx, func(tx *ent.Tx) error {
		incs, err := tx.Incident.Query().Where(incident.Muted(true), incident.MuteIDNotNil()).All(ctx)
		if err != nil {
			return err
		}
		seen := map[int]bool{}
		for _, inc := range incs {
			if seen[*inc.MuteID] {
				continue
			}
			seen[*inc.MuteID] = true
			m, err := tx.Mute.Get(ctx, *inc.MuteID)
			if err != nil && !ent.IsNotFound(err) {
				return err
			}
			if m != nil && m.EndsAt.After(now) {
				continue
			}
			if m == nil {
				m = &ent.Mute{ID: *inc.MuteID}
			}
			if err := s.unmuteFor(ctx, tx, m, domain.System); err != nil {
				return err
			}
		}
		return nil
	})
}

// applyActiveMutes mutes inc if an active mute matches.
func (s *Service) applyActiveMutes(ctx context.Context, tx *ent.Tx, inc *ent.Incident) (*ent.Incident, error) {
	if inc.Muted {
		return inc, nil
	}
	now := s.now()
	mutes, err := tx.Mute.Query().Where(muteActiveAt(now)...).All(ctx)
	if err != nil {
		return nil, err
	}
	labels := incidentMatchLabels(inc)
	for _, m := range mutes {
		if domain.MatchAll(m.Matchers, labels) {
			inc, err = tx.Incident.UpdateOne(inc).SetMuted(true).SetMuteID(m.ID).Save(ctx)
			if err != nil {
				return nil, err
			}
			_, err = s.addEvent(ctx, tx, inc, domain.EventMuteCreated, domain.System, map[string]any{
				"mute_id": m.ID, "ends_at": m.EndsAt, "reason": m.Reason, "matchers": matchersString(m.Matchers), "existing": true,
			})
			return inc, err
		}
	}
	return inc, nil
}

// incidentMatchLabels are the labels mutes and routes match on.
func incidentMatchLabels(inc *ent.Incident) map[string]string {
	l := make(map[string]string, len(inc.Labels)+4)
	for k, v := range inc.Labels {
		l[k] = v
	}
	if inc.Cluster != "" {
		l["cluster"] = inc.Cluster
	}
	if inc.Namespace != "" {
		l["namespace"] = inc.Namespace
	}
	l["severity"] = inc.Severity
	if inc.Source != "" {
		l["source"] = inc.Source
	}
	return l
}

func matchersString(ms []domain.Matcher) string {
	out := ""
	for i, m := range ms {
		if i > 0 {
			out += ", "
		}
		out += m.String()
	}
	return out
}
