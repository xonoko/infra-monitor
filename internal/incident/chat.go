package incident

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/message"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// PostMessage adds a user message to the incident conversation and asks the agent to reply.
func (s *Service) PostMessage(ctx context.Context, actor domain.Actor, incidentID int, content string) (*ent.Message, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("%w: empty message", ErrInvalid)
	}
	if len(content) > 8000 {
		return nil, fmt.Errorf("%w: message too long (max 8000 characters)", ErrInvalid)
	}
	var msg *ent.Message
	err := s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		channel := actor.Channel
		if channel == "" {
			channel = domain.OriginUI
		}
		msg, err = tx.Message.Create().SetIncidentID(inc.ID).SetRole(message.RoleUser).
			SetNillableAuthorID(actor.UserIDPtr()).SetAuthorName(actor.Name).
			SetChannel(channel).SetChannelName(actor.ChannelName).SetContent(content).Save(ctx)
		if err != nil {
			return err
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventMessage, actor, map[string]any{
			"message_id": msg.ID, "role": "user", "content": truncate(content, 2000),
		}); err != nil {
			return err
		}
		if s.agent == nil {
			return nil
		}
		return enqueue(ctx, tx, JobChatReply, chatPayload{IncidentID: inc.ID, MessageID: msg.ID}, queue.Opts{MaxAttempts: 2})
	})
	return msg, err
}

func (s *Service) runChatReply(ctx context.Context, p chatPayload) error {
	if s.agent == nil {
		return nil
	}
	inc, err := s.ent.Incident.Get(ctx, p.IncidentID)
	if err != nil {
		return notFoundPermanent(err)
	}
	userMsg, err := s.ent.Message.Get(ctx, p.MessageID)
	if err != nil {
		return notFoundPermanent(err)
	}
	if key, reason, err := s.capReason(ctx); err != nil {
		return err
	} else if key != "" {
		s.m.skipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", key)))
		return s.chatRefused(ctx, inc.ID, userMsg.ID, reason)
	}
	ic, err := s.BuildContext(ctx, inc)
	if err != nil {
		return err
	}
	actx, cancel := context.WithTimeout(ctx, s.cfg.Agent.Timeout)
	defer cancel()
	actx, span := telemetry.Tracer().Start(actx, "agent.chat")
	span.SetAttributes(attribute.Int("incident.id", inc.ID), attribute.String("agent", s.agent.Name()))
	if s.Typing != nil {
		s.Typing(ctx, inc.ID)
	}
	res, aerr := s.agent.Chat(actx, agent.ChatRequest{
		Incident: ic, Conversation: inc.AgentConversation, Message: userMsg.Content, Author: userMsg.AuthorName,
	}, s.progressFunc(ctx, inc.ID))
	telemetry.RecordError(span, aerr)
	span.End()
	s.publishProgress(ctx, Progress{IncidentID: inc.ID, Kind: "done"})

	if aerr == nil {
		s.recordUsage(ctx, "chat", res.Model, res.Usage)
	}
	actor := domain.Agent
	actor.Name = s.agent.Name()
	ctx = context.WithoutCancel(ctx)
	return s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, inc.ID)
		if err != nil {
			return err
		}
		if aerr != nil {
			s.log.WarnContext(ctx, "agent chat failed", "incident_id", inc.ID, "error", aerr)
			m, err := tx.Message.Create().SetIncidentID(inc.ID).SetRole(message.RoleSystem).SetAuthorName("infra-monitor").
				SetChannel(domain.OriginSystem).SetStatus(message.StatusFailed).SetReplyToID(userMsg.ID).
				SetContent(chatFailureText(aerr)).Save(ctx)
			if err != nil {
				return err
			}
			_, err = s.addEvent(ctx, tx, inc, domain.EventMessage, domain.System, map[string]any{
				"message_id": m.ID, "role": "system", "content": m.Content, "failed": true,
			})
			return err
		}
		m, err := tx.Message.Create().SetIncidentID(inc.ID).SetRole(message.RoleAgent).SetAuthorName(actor.Name).
			SetChannel(domain.OriginSystem).SetContent(firstNonEmpty(res.Reply, "(empty reply)")).SetReplyToID(userMsg.ID).
			SetModel(res.Model).SetPromptTokens(res.Usage.PromptTokens).SetCompletionTokens(res.Usage.CompletionTokens).
			SetCostUsd(s.costUSD(res.Model, res.Usage)).Save(ctx)
		if err != nil {
			return err
		}
		if len(res.Conversation) > 0 && json.Valid(res.Conversation) {
			if inc, err = tx.Incident.UpdateOne(inc).SetAgentConversation(res.Conversation).Save(ctx); err != nil {
				return err
			}
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventMessage, actor, map[string]any{
			"message_id": m.ID, "role": "agent", "content": truncate(m.Content, 2000), "reply_to": userMsg.ID,
		}); err != nil {
			return err
		}
		ids, err := s.proposeFromAgent(ctx, tx, inc, res.ProposedActions, nil, actor)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			return tx.Message.UpdateOne(m).SetActionIds(ids).Exec(ctx)
		}
		return nil
	})
}

func chatFailureText(err error) string {
	if agent.IsBudgetExceeded(err) {
		return budgetPrefix + "the agent can't answer right now (" + truncate(err.Error(), 300) + ")"
	}
	return "The agent could not answer: " + truncate(err.Error(), 500)
}

// chatRefused answers a chat message with a system message when caps or the budget block the agent.
func (s *Service) chatRefused(ctx context.Context, incID, userMsgID int, reason string) error {
	return s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, incID)
		if err != nil {
			return err
		}
		m, err := tx.Message.Create().SetIncidentID(inc.ID).SetRole(message.RoleSystem).SetAuthorName("infra-monitor").
			SetChannel(domain.OriginSystem).SetStatus(message.StatusFailed).SetReplyToID(userMsgID).
			SetContent("The agent is not available: " + reason).Save(ctx)
		if err != nil {
			return err
		}
		_, err = s.addEvent(ctx, tx, inc, domain.EventMessage, domain.System, map[string]any{
			"message_id": m.ID, "role": "system", "content": m.Content, "failed": true,
		})
		return err
	})
}
