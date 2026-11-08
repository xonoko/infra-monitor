// Package pubsub fans PostgreSQL NOTIFY messages out to in-process subscribers.
// Each replica runs one Listener on a dedicated connection, so a timeline insert on any
// replica reaches the SSE clients and workers of every replica.
package pubsub

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ChannelTimeline = "im_timeline"
	ChannelJobs     = "im_jobs"
	ChannelStream   = "im_stream" // ephemeral agent progress, not persisted
)

// Reconnected is delivered to every subscriber after the listener reconnects,
// so they can resync state that may have been missed.
const Reconnected = "\x00reconnected"

type Listener struct {
	pool     *pgxpool.Pool
	log      *slog.Logger
	channels []string

	mu   sync.RWMutex
	subs map[string]map[chan string]struct{}
}

func NewListener(pool *pgxpool.Pool, log *slog.Logger, channels ...string) *Listener {
	if len(channels) == 0 {
		channels = []string{ChannelTimeline, ChannelJobs, ChannelStream}
	}
	return &Listener{pool: pool, log: log, channels: channels, subs: map[string]map[chan string]struct{}{}}
}

// Subscribe returns a buffered channel of payloads. Slow subscribers drop messages.
func (l *Listener) Subscribe(channel string, buffer int) (<-chan string, func()) {
	ch := make(chan string, buffer)
	l.mu.Lock()
	if l.subs[channel] == nil {
		l.subs[channel] = map[chan string]struct{}{}
	}
	l.subs[channel][ch] = struct{}{}
	l.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.subs[channel], ch)
			l.mu.Unlock()
		})
	}
}

func (l *Listener) publish(channel, payload string) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for ch := range l.subs[channel] {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (l *Listener) broadcast(payload string) {
	for _, c := range l.channels {
		l.publish(c, payload)
	}
}

// Run listens until ctx is done, reconnecting with backoff.
func (l *Listener) Run(ctx context.Context) error {
	backoff := time.Second
	first := true
	for ctx.Err() == nil {
		err := l.listen(ctx, func() {
			if !first {
				l.broadcast(Reconnected)
			}
			first = false
			backoff = time.Second
		})
		if ctx.Err() != nil {
			return nil
		}
		l.log.Warn("listener disconnected", "error", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	return nil
}

func (l *Listener) listen(ctx context.Context, onReady func()) error {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// The connection is in LISTEN state: never return it to the pool.
	pc := conn.Hijack()
	defer func() { _ = pc.Close(context.Background()) }()
	for _, c := range l.channels {
		if _, err := pc.Exec(ctx, "LISTEN "+pgx.Identifier{c}.Sanitize()); err != nil {
			return err
		}
	}
	onReady()
	for {
		n, err := pc.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		l.publish(n.Channel, n.Payload)
	}
}

// Notify sends a NOTIFY outside of a transaction.
func Notify(ctx context.Context, pool *pgxpool.Pool, channel, payload string) error {
	_, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	return err
}
