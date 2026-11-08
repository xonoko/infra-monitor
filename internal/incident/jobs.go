package incident

import (
	"context"
	"time"

	"github.com/xonoko/infra-monitor/internal/queue"
)

// Job kinds.
const (
	JobProcessSignal   = "process_signal"
	JobInvestigate     = "investigate_incident"
	JobFailOpen        = "notify_failopen"
	JobResolveIncident = "resolve_incident"
	JobChatReply       = "agent_chat_reply"
	JobExecuteAction   = "execute_action"
	JobHeartbeatPing   = "heartbeat_ping"
	// JobChannelSync is handled by the channel dispatcher.
	JobChannelSync = "channel_sync"
)

type processSignalPayload struct {
	SignalID   int    `json:"signal_id"`
	Transition string `json:"transition"` // new | refire | repeat | resolved | stale
}

type incidentPayload struct {
	IncidentID  int    `json:"incident_id"`
	Trigger     string `json:"trigger,omitempty"`
	RequestedBy int    `json:"requested_by,omitempty"`
}

type chatPayload struct {
	IncidentID int `json:"incident_id"`
	MessageID  int `json:"message_id"`
}

type actionPayload struct {
	ActionID int `json:"action_id"`
}

type heartbeatPayload struct {
	Key string `json:"key"`
}

// ChannelSyncPayload is the payload of JobChannelSync.
type ChannelSyncPayload struct {
	IncidentID int    `json:"incident_id"`
	Channel    string `json:"channel"`
}

// RegisterJobs registers the service's job handlers.
func (s *Service) RegisterJobs(q *queue.Queue) {
	q.Register(JobProcessSignal, func(ctx context.Context, j *queue.Job) error {
		var p processSignalPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return s.ProcessSignal(ctx, p.SignalID, p.Transition)
	}, queue.HandlerOpts{Concurrency: 4, Timeout: time.Minute})

	agentTimeout := s.cfg.Agent.Timeout + time.Minute
	q.Register(JobInvestigate, func(ctx context.Context, j *queue.Job) error {
		var p incidentPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return s.runInvestigation(ctx, p)
	}, queue.HandlerOpts{Concurrency: max(1, s.cfg.Agent.MaxConcurrent), Timeout: agentTimeout})

	q.Register(JobFailOpen, func(ctx context.Context, j *queue.Job) error {
		var p incidentPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return s.failOpen(ctx, p.IncidentID)
	}, queue.HandlerOpts{Concurrency: 2, Timeout: time.Minute})

	q.Register(JobResolveIncident, func(ctx context.Context, j *queue.Job) error {
		var p incidentPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return s.resolveIfAllResolved(ctx, p.IncidentID)
	}, queue.HandlerOpts{Concurrency: 2, Timeout: time.Minute})

	q.Register(JobChatReply, func(ctx context.Context, j *queue.Job) error {
		var p chatPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return s.runChatReply(ctx, p)
	}, queue.HandlerOpts{Concurrency: max(1, s.cfg.Agent.MaxConcurrent), Timeout: agentTimeout})

	q.Register(JobExecuteAction, func(ctx context.Context, j *queue.Job) error {
		var p actionPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return s.executeAction(ctx, p.ActionID)
	}, queue.HandlerOpts{Concurrency: 2, Timeout: s.cfg.Actions.Timeout + time.Minute})

	q.Register(JobHeartbeatPing, func(ctx context.Context, j *queue.Job) error {
		var p heartbeatPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return s.pingHeartbeat(ctx, p.Key)
	}, queue.HandlerOpts{Concurrency: 1, Timeout: 30 * time.Second})
}
