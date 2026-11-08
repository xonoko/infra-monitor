// Package app wires infra-monitor together for the `serve` command.
// Construction is explicit (no DI container): every component is built from plain
// constructors in dependency order, then long-running loops run in one errgroup.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/xonoko/infra-monitor/internal/api"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/lease"
	"github.com/xonoko/infra-monitor/internal/pubsub"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
	"github.com/xonoko/infra-monitor/web"
)

type App struct {
	Cfg       *config.Config
	Log       *slog.Logger
	Telemetry *telemetry.Telemetry
	DB        *db.DB
	Listener  *pubsub.Listener
	Queue     *queue.Queue
	Leases    *lease.Manager
	Server    *api.Server

	// runners are long-running loops started by Run.
	runners []runner
	closers []func(context.Context) error
}

type runner struct {
	name string
	fn   func(ctx context.Context) error
}

func (a *App) addRunner(name string, fn func(ctx context.Context) error) {
	a.runners = append(a.runners, runner{name: name, fn: fn})
}

// Options tweak Build for tests.
type Options struct {
	SkipMigrate bool
}

// Build constructs every component. Call Close when done.
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger, opts Options) (_ *App, err error) {
	a := &App{Cfg: cfg, Log: log}
	defer func() {
		if err != nil {
			a.Close(context.Background())
		}
	}()

	a.Telemetry, err = telemetry.Setup(ctx, cfg.Telemetry, log)
	if err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	a.closers = append(a.closers, a.Telemetry.Shutdown)

	a.DB, err = db.Open(ctx, cfg.Database, log)
	if err != nil {
		return nil, err
	}
	a.closers = append(a.closers, func(context.Context) error { a.DB.Close(); return nil })

	if cfg.Database.MigrateOnStart && !opts.SkipMigrate {
		if err := db.Migrate(ctx, a.DB.Pool, log); err != nil {
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}

	a.Listener = pubsub.NewListener(a.DB.Pool, log)
	a.addRunner("listener", a.Listener.Run)
	a.Queue = queue.New(a.DB.Pool, a.Listener, log, cfg.Jobs.PollInterval)
	a.addRunner("queue", a.Queue.Run)
	a.Leases = lease.NewManager(a.DB.Pool, log)

	if err := a.buildServices(ctx); err != nil {
		return nil, err
	}

	a.Server = api.New(cfg, log, a.apiDeps())
	return a, nil
}

func (a *App) apiDeps() api.Deps {
	d := api.Deps{DB: a.DB, Metrics: a.Telemetry.MetricsHandler(), UI: web.Dist()}
	a.fillAPIDeps(&d)
	return d
}

// Run serves HTTP and runs every loop until ctx is cancelled, then shuts down gracefully.
func (a *App) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.Cfg.Server.Listen)
	if err != nil {
		return err
	}
	return a.Serve(ctx, ln)
}

// Serve is Run with a provided listener (tests use a random port).
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	g, gctx := errgroup.WithContext(ctx)
	srv := &http.Server{Handler: a.Server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	g.Go(func() error {
		a.Log.Info("http server listening", "addr", ln.Addr().String(), "public_url", a.Cfg.Server.PublicURL)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), a.Cfg.Server.ShutdownTimeout)
		defer cancel()
		a.Log.Info("shutting down http server")
		return srv.Shutdown(sctx)
	})
	for _, r := range a.runners {
		g.Go(func() error {
			if err := r.fn(gctx); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("%s: %w", r.name, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// Close releases resources in reverse order.
func (a *App) Close(ctx context.Context) {
	for i := len(a.closers) - 1; i >= 0; i-- {
		if err := a.closers[i](ctx); err != nil {
			a.Log.Warn("close failed", "error", err)
		}
	}
}
