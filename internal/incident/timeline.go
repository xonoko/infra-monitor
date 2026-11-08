package incident

import (
	"context"
	"fmt"

	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/timelineevent"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// addEvent writes a timeline event and schedules a sync of every channel the incident
// is routed to, in the same transaction.
func (s *Service) addEvent(ctx context.Context, tx *ent.Tx, inc *ent.Incident, typ string, actor domain.Actor, payload map[string]any) (*ent.TimelineEvent, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	ev, err := tx.TimelineEvent.Create().
		SetIncidentID(inc.ID).
		SetType(typ).
		SetActorType(timelineevent.ActorType(eventActorType(actor))).
		SetNillableActorID(actor.UserIDPtr()).
		SetActorName(actor.Name).
		SetChannel(actor.Channel).
		SetChannelName(actor.ChannelName).
		SetPayload(payload).
		SetTraceID(telemetry.TraceID(ctx)).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("timeline %s: %w", typ, err)
	}
	if err := s.syncChannels(ctx, tx, inc.ID, inc.NotifyChannels); err != nil {
		return nil, err
	}
	return ev, nil
}

func (s *Service) syncChannels(ctx context.Context, tx *ent.Tx, incidentID int, channels []string) error {
	for _, ch := range channels {
		if err := enqueue(ctx, tx, JobChannelSync, ChannelSyncPayload{IncidentID: incidentID, Channel: ch},
			queue.Opts{UniqueKey: fmt.Sprintf("sync:%s:%d", ch, incidentID), MaxAttempts: 8}); err != nil {
			return err
		}
	}
	return nil
}

func eventActorType(a domain.Actor) string {
	switch a.Type {
	case domain.ActorUser:
		return "user"
	case domain.ActorAgent:
		return "agent"
	default:
		return "system"
	}
}
