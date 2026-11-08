// Package lease runs singleton work (exactly one replica) using PostgreSQL session
// advisory locks held on a dedicated connection. If the connection dies, the lock is
// released by Postgres and the work context is canceled here.
package lease

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Manager struct {
	pool          *pgxpool.Pool
	log           *slog.Logger
	RetryInterval time.Duration
	CheckInterval time.Duration

	mu   sync.Mutex
	held map[string]bool
}

func NewManager(pool *pgxpool.Pool, log *slog.Logger) *Manager {
	m := &Manager{pool: pool, log: log, RetryInterval: 10 * time.Second, CheckInterval: 5 * time.Second, held: map[string]bool{}}
	meter := otel.Meter("github.com/xonoko/infra-monitor/lease")
	_, _ = meter.Int64ObservableGauge("infra_monitor_lease_held",
		metric.WithDescription("1 when this replica holds the named lease"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			for name, h := range m.held {
				v := int64(0)
				if h {
					v = 1
				}
				o.Observe(v, metric.WithAttributes(attribute.String("lease", name)))
			}
			return nil
		}))
	return m
}

// Key maps a lease name to an advisory lock key.
func Key(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("infra-monitor:" + name))
	return int64(h.Sum64())
}

// Held reports whether this replica currently holds the lease.
func (m *Manager) Held(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.held[name]
}

func (m *Manager) setHeld(name string, v bool) {
	m.mu.Lock()
	m.held[name] = v
	m.mu.Unlock()
}

// Run blocks until ctx is done. Whenever it holds the lease it runs fn with a context that is
// canceled when the lease is lost. fn should return when its context is done.
func (m *Manager) Run(ctx context.Context, name string, fn func(ctx context.Context) error) {
	m.setHeld(name, false)
	for ctx.Err() == nil {
		acquired, err := m.runOnce(ctx, name, fn)
		if err != nil && !errors.Is(err, context.Canceled) {
			m.log.Warn("lease run ended with error", "lease", name, "error", err)
		}
		if acquired && ctx.Err() == nil {
			// Lost or released: retry quickly to let another replica take over or to reacquire.
			sleep(ctx, time.Second)
			continue
		}
		sleep(ctx, m.RetryInterval)
	}
}

func (m *Manager) runOnce(ctx context.Context, name string, fn func(ctx context.Context) error) (bool, error) {
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	key := Key(name)
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
		conn.Release()
		return false, err
	}
	if !ok {
		conn.Release()
		return false, nil
	}
	m.setHeld(name, true)
	m.log.Info("lease acquired", "lease", name)
	workCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- fn(workCtx) }()

	ticker := time.NewTicker(m.CheckInterval)
	defer ticker.Stop()
	var runErr error
	lost := false
loop:
	for {
		select {
		case runErr = <-done:
			break loop
		case <-ticker.C:
			pctx, pcancel := context.WithTimeout(ctx, 3*time.Second)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil && ctx.Err() == nil {
				m.log.Warn("lease connection lost, stopping singleton work", "lease", name, "error", err)
				lost = true
				cancel()
				runErr = <-done
				break loop
			}
		case <-ctx.Done():
			cancel()
			runErr = <-done
			break loop
		}
	}
	cancel()
	m.setHeld(name, false)
	if lost {
		c := conn.Hijack()
		_ = c.Close(context.Background())
	} else {
		uctx, ucancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", key)
		ucancel()
		if err != nil {
			c := conn.Hijack()
			_ = c.Close(context.Background())
		} else {
			conn.Release()
		}
	}
	m.log.Info("lease released", "lease", name)
	return true, runErr
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// HeldNames lists the leases this replica holds.
func (m *Manager) HeldNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for n, h := range m.held {
		if h {
			out = append(out, n)
		}
	}
	return out
}
