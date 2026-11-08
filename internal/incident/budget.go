package incident

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/db/ent/investigation"
	"github.com/xonoko/infra-monitor/internal/db/ent/message"
)

// Cost controls. An alert storm must never produce a large LLM bill:
//   - automatic investigations only for configured severities (default critical, warning)
//   - storm mode groups bursts of incidents into one investigation (storm.go)
//   - hard caps on agent calls (investigations + chat replies): concurrent, per hour, per day
//   - a budget / rate-limit error from the LLM stack is final and pauses every agent call
//     for agent.budget_pause; incidents fail open ("AI budget reached")

const budgetPrefix = "AI budget reached: "

// Skip reasons (metric label + note prefix).
const (
	SkipSeverity = "severity"
	SkipStorm    = "storm"
	SkipCap      = "cap"
	SkipBudget   = "budget"
	SkipCooldown = "cooldown"
	SkipState    = "state"
)

// AgentUsage summarizes recent agent usage (status page, caps).
type AgentUsage struct {
	CallsLastHour int
	CallsToday    int
	PausedUntil   *time.Time
	MaxPerHour    int
	MaxPerDay     int
}

// agentCalls counts agent calls since t: investigations started plus chat replies (incl. failures).
func (s *Service) agentCalls(ctx context.Context, since time.Time) (int, error) {
	inv, err := s.ent.Investigation.Query().Where(investigation.CreatedAtGT(since)).Count(ctx)
	if err != nil {
		return 0, err
	}
	chat, err := s.ent.Message.Query().Where(message.CreatedAtGT(since), message.ReplyToIDNotNil(),
		message.RoleIn(message.RoleAgent, message.RoleSystem)).Count(ctx)
	if err != nil {
		return 0, err
	}
	return inv + chat, nil
}

// budgetPausedUntil returns when the pause after the last budget error ends (nil = not paused).
func (s *Service) budgetPausedUntil(ctx context.Context) (*time.Time, error) {
	pause := s.cfg.Agent.BudgetPause
	if pause <= 0 {
		return nil, nil
	}
	since := s.now().Add(-pause)
	var last time.Time
	inv, err := s.ent.Investigation.Query().Where(investigation.ErrorHasPrefix(budgetPrefix), investigation.UpdatedAtGT(since)).
		Order(investigation.ByUpdatedAt()).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range inv {
		if i.UpdatedAt.After(last) {
			last = i.UpdatedAt
		}
	}
	msgs, err := s.ent.Message.Query().Where(message.RoleEQ(message.RoleSystem), message.ContentHasPrefix(budgetPrefix), message.CreatedAtGT(since)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		if m.CreatedAt.After(last) {
			last = m.CreatedAt
		}
	}
	if last.IsZero() {
		return nil, nil
	}
	until := last.Add(pause)
	return &until, nil
}

// Usage reports agent usage against the caps.
func (s *Service) Usage(ctx context.Context) (AgentUsage, error) {
	now := s.now()
	u := AgentUsage{MaxPerHour: s.cfg.Agent.MaxPerHour, MaxPerDay: s.cfg.Agent.MaxPerDay}
	var err error
	if u.CallsLastHour, err = s.agentCalls(ctx, now.Add(-time.Hour)); err != nil {
		return u, err
	}
	if u.CallsToday, err = s.agentCalls(ctx, now.Add(-24*time.Hour)); err != nil {
		return u, err
	}
	u.PausedUntil, err = s.budgetPausedUntil(ctx)
	return u, err
}

// capReason returns a non-empty reason when an agent call must not happen now.
func (s *Service) capReason(ctx context.Context) (key, reason string, err error) {
	until, err := s.budgetPausedUntil(ctx)
	if err != nil {
		return "", "", err
	}
	if until != nil {
		return SkipBudget, fmt.Sprintf("%spaused until %s after a budget/rate-limit error", budgetPrefix, until.UTC().Format("15:04 UTC")), nil
	}
	now := s.now()
	if m := s.cfg.Agent.MaxPerHour; m > 0 {
		n, err := s.agentCalls(ctx, now.Add(-time.Hour))
		if err != nil {
			return "", "", err
		}
		if n >= m {
			return SkipCap, fmt.Sprintf("hourly AI cap reached (%d calls)", m), nil
		}
	}
	if m := s.cfg.Agent.MaxPerDay; m > 0 {
		n, err := s.agentCalls(ctx, now.Add(-24*time.Hour))
		if err != nil {
			return "", "", err
		}
		if n >= m {
			return SkipCap, fmt.Sprintf("daily AI cap reached (%d calls)", m), nil
		}
	}
	return "", "", nil
}

func (s *Service) severityAllowed(sev string) bool {
	allowed := s.cfg.Agent.InvestigateSeverities
	return len(allowed) == 0 || slices.Contains(allowed, strings.ToLower(sev))
}

// recordUsage updates token and cost metrics.
func (s *Service) recordUsage(ctx context.Context, kind, model string, u agent.Usage) {
	attrs := metric.WithAttributes(attribute.String("kind", kind), attribute.String("model", model))
	s.m.tokens.Add(ctx, int64(u.PromptTokens), metric.WithAttributes(attribute.String("type", "prompt"), attribute.String("model", model)))
	s.m.tokens.Add(ctx, int64(u.CompletionTokens), metric.WithAttributes(attribute.String("type", "completion"), attribute.String("model", model)))
	cost := float64(u.PromptTokens)/1e6*s.cfg.Agent.PriceInputPerMTok + float64(u.CompletionTokens)/1e6*s.cfg.Agent.PriceOutputPerMTok
	s.m.cost.Add(ctx, cost, attrs)
}
