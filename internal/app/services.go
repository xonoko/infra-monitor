package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/actions/amsilence"
	"github.com/xonoko/infra-monitor/internal/actions/k8s"
	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/agent/holmes"
	"github.com/xonoko/infra-monitor/internal/api"
	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/channel"
	"github.com/xonoko/infra-monitor/internal/channel/dispatch"
	"github.com/xonoko/infra-monitor/internal/channel/telegram"
	"github.com/xonoko/infra-monitor/internal/incident"
	"github.com/xonoko/infra-monitor/internal/source/alertmanager"
)

// services holds the domain components built by buildServices.
type services struct {
	auth       *auth.Service
	incidents  *incident.Service
	dispatcher *dispatch.Dispatcher
	amClients  map[string]*alertmanager.Client
}

func (a *App) buildServices(ctx context.Context) error {
	cfg := a.Cfg
	a.svc = &services{amClients: map[string]*alertmanager.Client{}}

	silencers := map[string]incident.Silencer{}
	for _, src := range cfg.Sources {
		if src.Type != "alertmanager" || src.AlertmanagerURL == "" {
			continue
		}
		c, err := alertmanager.NewClient(src.AlertmanagerURL, nil)
		if err != nil {
			return fmt.Errorf("source %s: %w", src.Name, err)
		}
		a.svc.amClients[src.Name] = c
		silencers[src.Name] = c
	}

	reg := actions.NewRegistry(cfg.Actions.Enabled, cfg.Actions.AutoApprove)
	k8sClients, err := k8s.NewClients(cfg.Actions)
	if err != nil {
		return fmt.Errorf("k8s clients: %w", err)
	}
	for _, e := range k8s.Executors(k8sClients, k8s.PolicyFromConfig(cfg.Actions)) {
		if err := reg.Register(e); err != nil {
			return err
		}
	}
	if err := reg.Register(amsilence.New(func(name string) (*alertmanager.Client, bool) {
		c, ok := a.svc.amClients[name]
		return c, ok
	}, cfg.Actions.MaxSilence)); err != nil {
		return err
	}

	var ag agent.Agent
	if cfg.Agent.Enabled {
		switch cfg.Agent.Type {
		case "holmes", "":
			h, err := holmes.New(cfg.Agent, a.Log)
			if err != nil {
				return fmt.Errorf("holmes: %w", err)
			}
			ag = h
		default:
			return fmt.Errorf("unsupported agent type %q", cfg.Agent.Type)
		}
	}

	a.svc.incidents, err = incident.New(incident.Deps{Config: cfg, DB: a.DB, Log: a.Log, Registry: reg, Agent: ag, Silencers: silencers,
		NamespaceAnnotations: k8s.NewNamespaceAnnotations(k8sClients)})
	if err != nil {
		return err
	}
	if err := reg.Register(incident.ReinvestigateExecutor{Svc: a.svc.incidents}); err != nil {
		return err
	}
	a.svc.auth = auth.NewService(cfg, a.DB, a.Log)

	var chans []channel.Channel
	for _, cc := range cfg.Channels {
		switch cc.Type {
		case "telegram":
			tg, err := telegram.New(cc, cfg.Server.PublicURL, a.Log)
			if err != nil {
				return fmt.Errorf("channel %s: %w", cc.Name, err)
			}
			chans = append(chans, tg)
		default:
			return fmt.Errorf("channel %s: unsupported type %q", cc.Name, cc.Type)
		}
	}
	a.svc.dispatcher = dispatch.New(a.DB, a.svc.incidents, a.svc.auth, a.Leases, a.Log, chans...)

	a.svc.incidents.RegisterJobs(a.Queue)
	a.svc.dispatcher.RegisterJobs(a.Queue)
	a.addRunner("channels", a.svc.dispatcher.Run)
	a.addRunner("scheduler", func(ctx context.Context) error {
		a.Leases.Run(ctx, "scheduler", a.runScheduler)
		return nil
	})
	if err := a.svc.incidents.EnsureHeartbeats(ctx); err != nil {
		return fmt.Errorf("heartbeats: %w", err)
	}
	return nil
}

func (a *App) fillAPIDeps(d *api.Deps) {
	d.Auth = a.svc.auth
	d.Incidents = a.svc.incidents
	d.Dispatcher = a.svc.dispatcher
	d.Listener = a.Listener
	d.Leases = a.Leases
	d.Parsers = map[string]api.WebhookParser{"alertmanager": alertmanager.ParseWebhook}
	d.Status = statusProvider{a}
}

type statusProvider struct{ a *App }

func (s statusProvider) HeldLeases() []string { return s.a.Leases.HeldNames() }
func (s statusProvider) Replica() string {
	h, _ := os.Hostname()
	return h
}

// runScheduler runs periodic maintenance on the replica holding the "scheduler" lease.
func (a *App) runScheduler(ctx context.Context) error {
	svc := a.svc.incidents
	fast := time.NewTicker(30 * time.Second)
	defer fast.Stop()
	slow := time.NewTicker(time.Hour)
	defer slow.Stop()
	nextReconcile := map[string]time.Time{}
	run := func(name string, fn func(context.Context) error) {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			a.Log.WarnContext(ctx, "scheduled task failed", "task", name, "error", err)
		}
	}
	tick := func() {
		run("heartbeats", svc.CheckHeartbeats)
		run("expire_proposals", svc.ExpireProposals)
		run("end_mutes", svc.EndExpiredMutes)
		run("resolve_storms", svc.ResolveStorms)
		for _, src := range a.Cfg.Sources {
			c, ok := a.svc.amClients[src.Name]
			if !ok || time.Now().Before(nextReconcile[src.Name]) {
				continue
			}
			nextReconcile[src.Name] = time.Now().Add(src.ReconcileInterval)
			run("reconcile:"+src.Name, func(ctx context.Context) error {
				rctx, cancel := context.WithTimeout(ctx, time.Minute)
				defer cancel()
				active, err := c.ListAlerts(rctx, src.Name)
				if err != nil {
					return err
				}
				added, resolved, err := svc.ReconcileSource(rctx, src.Name, active, 2*src.ReconcileInterval)
				if err == nil && (added > 0 || resolved > 0) {
					a.Log.InfoContext(ctx, "source reconciled", "source", src.Name, "added", added, "resolved", resolved)
				}
				return err
			})
		}
	}
	housekeeping := func() {
		run("retention", func(ctx context.Context) error { _, err := svc.Retention(ctx); return err })
		run("jobs_cleanup", func(ctx context.Context) error { _, err := a.Queue.Cleanup(ctx, a.Cfg.Retention.Jobs); return err })
		run("auth_cleanup", a.svc.auth.Cleanup)
		run("inbound_cleanup", a.svc.dispatcher.CleanupInbound)
	}
	tick()
	housekeeping()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-fast.C:
			tick()
		case <-slow.C:
			housekeeping()
		}
	}
}
