package api

import (
	"context"
	"io/fs"
	"net/http"

	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/channel/dispatch"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/incident"
	"github.com/xonoko/infra-monitor/internal/lease"
	"github.com/xonoko/infra-monitor/internal/pubsub"
)

// WebhookParser turns a source webhook body into signals.
type WebhookParser func(body []byte, sourceName string) ([]domain.Signal, error)

// StatusProvider adds runtime info to GET /status.
type StatusProvider interface {
	HeldLeases() []string
	Replica() string
}

// Deps are the services the API exposes. Nil services disable their routes.
type Deps struct {
	DB         Pinger
	Metrics    http.Handler
	UI         fs.FS
	Auth       *auth.Service
	Incidents  *incident.Service
	Dispatcher *dispatch.Dispatcher
	Listener   *pubsub.Listener
	Leases     *lease.Manager
	Parsers    map[string]WebhookParser // by source type
	Status     StatusProvider
	// Ping is used by readyz when DB is nil.
	Ping func(ctx context.Context) error
}

// FullDepsForSpec returns placeholder deps so every route is registered when dumping the spec.
func FullDepsForSpec() Deps {
	return Deps{Auth: &auth.Service{}, Incidents: &incident.Service{}, Dispatcher: &dispatch.Dispatcher{}, Listener: &pubsub.Listener{},
		Parsers: map[string]WebhookParser{}}
}
