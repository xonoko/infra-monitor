// Package incident is the core service: ingestion, correlation, heartbeats, the unified
// timeline, investigations, chat, actions, mutes and notification routing.
//
// Every mutation writes a TimelineEvent in the same transaction. The API, the UI and every
// chat channel call these same methods with a domain.Actor, so permissions and audit are
// identical whatever the origin.
package incident

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// Errors mapped to HTTP statuses by the API.
var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid request")
)

// Silencer creates Alertmanager silences for mirrored mutes (implemented by the alertmanager client).
type Silencer interface {
	CreateSilence(ctx context.Context, matchers []domain.Matcher, startsAt, endsAt time.Time, createdBy, comment string) (string, error)
	ExpireSilence(ctx context.Context, id string) error
}

type Deps struct {
	Config   *config.Config
	DB       *db.DB
	Log      *slog.Logger
	Registry *actions.Registry
	Agent    agent.Agent // nil when disabled
	// Silencers by source name (optional).
	Silencers map[string]Silencer
	// NamespaceAnnotations overrides the auto-approve policy per namespace (optional).
	NamespaceAnnotations NamespaceAnnotations
	Now                  func() time.Time
}

type Service struct {
	cfg           *config.Config
	db            *db.DB
	ent           *ent.Client
	log           *slog.Logger
	registry      *actions.Registry
	agent         agent.Agent
	silencers     map[string]Silencer
	nsAnnotations NamespaceAnnotations
	now           func() time.Time

	heartbeatMatchers []domain.Matcher
	routes            []route

	// Typing is called while the agent works on an incident (channels show "typing...").
	Typing func(ctx context.Context, incidentID int)

	m metrics
}

type metrics struct {
	signals        metric.Int64Counter
	incidents      metric.Int64Counter
	investigations metric.Int64Counter
	actions        metric.Int64Counter
	routed         metric.Int64Counter
	skipped        metric.Int64Counter
	tokens         metric.Int64Counter
	cost           metric.Float64Counter
}

func New(d Deps) (*Service, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &Service{
		cfg: d.Config, db: d.DB, ent: d.DB.Ent, log: d.Log, registry: d.Registry, agent: d.Agent,
		silencers: d.Silencers, now: d.Now,
		nsAnnotations: d.NamespaceAnnotations,
	}
	if s.registry == nil {
		s.registry = actions.NewRegistry(nil, nil)
	}
	if s.silencers == nil {
		s.silencers = map[string]Silencer{}
	}
	var err error
	if d.Config.Heartbeat.Enabled {
		if s.heartbeatMatchers, err = domain.ParseMatchers(d.Config.Heartbeat.Matchers); err != nil {
			return nil, fmt.Errorf("heartbeat.matchers: %w", err)
		}
	}
	if s.routes, err = compileRoutes(d.Config); err != nil {
		return nil, err
	}
	meter := otel.Meter(telemetry.ScopeName + "/incident")
	s.m.signals, _ = meter.Int64Counter("infra_monitor_signals_received", metric.WithDescription("Signals received by source and status"))
	s.m.incidents, _ = meter.Int64Counter("infra_monitor_incidents_opened", metric.WithDescription("Incidents opened or reopened"))
	s.m.investigations, _ = meter.Int64Counter("infra_monitor_investigations", metric.WithDescription("Investigations by outcome"))
	s.m.actions, _ = meter.Int64Counter("infra_monitor_actions", metric.WithDescription("Action transitions by type and status"))
	s.m.routed, _ = meter.Int64Counter("infra_monitor_notifications_routed", metric.WithDescription("Incidents routed to channels"))
	s.m.skipped, _ = meter.Int64Counter("infra_monitor_investigations_skipped", metric.WithDescription("Investigations not run, by reason (storm, cap, budget, severity, cooldown, state)"))
	s.m.tokens, _ = meter.Int64Counter("infra_monitor_agent_tokens", metric.WithDescription("LLM tokens reported by the agent"))
	s.m.cost, _ = meter.Float64Counter("infra_monitor_agent_cost_usd", metric.WithDescription("Estimated LLM cost (agent.price_*_per_mtok)"))
	_, _ = meter.Int64ObservableGauge("infra_monitor_open_incidents", metric.WithDescription("Open or acknowledged incidents"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			var n int64
			if err := s.db.Pool.QueryRow(ctx, "SELECT count(*) FROM incidents WHERE status IN ('open','acknowledged')").Scan(&n); err == nil {
				o.Observe(n)
			}
			return nil
		}))
	return s, nil
}

// Registry exposes the action registry.
func (s *Service) Registry() *actions.Registry { return s.registry }

// AgentEnabled reports whether an agent is configured.
func (s *Service) AgentEnabled() bool { return s.agent != nil }

// Agent returns the configured agent (may be nil).
func (s *Service) Agent() agent.Agent { return s.agent }

// IncidentURL is the UI link for an incident.
func (s *Service) IncidentURL(id int) string {
	return s.cfg.Server.PublicURL + "/incidents/" + strconv.Itoa(id)
}

func require(a domain.Actor, min domain.Role) error {
	if !a.Role.Allows(min) {
		return fmt.Errorf("%w: requires role %s", ErrForbidden, min)
	}
	return nil
}

func (s *Service) tx(ctx context.Context, fn func(tx *ent.Tx) error) error {
	return db.WithTx(ctx, s.ent, fn)
}

// enqueue is queue.Enqueue bound to a transaction.
func enqueue(ctx context.Context, tx *ent.Tx, kind string, payload any, o queue.Opts) error {
	return queue.Enqueue(ctx, tx, kind, payload, o)
}

func notFound(err error, what string) error {
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return err
}
