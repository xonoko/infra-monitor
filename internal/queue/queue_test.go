package queue_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/pubsub"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

func start(t *testing.T, d *db.DB, register func(q *queue.Queue)) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	l := pubsub.NewListener(d.Pool, pgtest.Logger())
	go l.Run(ctx)
	q := queue.New(d.Pool, l, pgtest.Logger(), 200*time.Millisecond)
	register(q)
	done := make(chan struct{})
	go func() { _ = q.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel
}

func TestProcessRetryAndUnique(t *testing.T) {
	d := pgtest.New(t)
	ctx := context.Background()
	var mu sync.Mutex
	got := map[string]int{}
	var flaky atomic.Int32
	start(t, d, func(q *queue.Queue) {
		q.Register("echo", func(ctx context.Context, j *queue.Job) error {
			var p struct{ V string }
			require.NoError(t, j.Decode(&p))
			mu.Lock()
			got[p.V]++
			mu.Unlock()
			return nil
		}, queue.HandlerOpts{})
		q.Register("flaky", func(ctx context.Context, j *queue.Job) error {
			if flaky.Add(1) < 2 {
				return errors.New("boom")
			}
			return nil
		}, queue.HandlerOpts{})
	})

	// unique: three enqueues with the same key -> one job
	for i := 0; i < 3; i++ {
		require.NoError(t, queue.Enqueue(ctx, d.Ent, "echo", map[string]string{"V": "u"}, queue.Opts{UniqueKey: "k1", Delay: 300 * time.Millisecond}))
	}
	require.NoError(t, queue.Enqueue(ctx, d.Ent, "echo", map[string]string{"V": "a"}, queue.Opts{}))
	require.NoError(t, queue.Enqueue(ctx, d.Ent, "flaky", struct{}{}, queue.Opts{}))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got["a"] == 1 && got["u"] == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { return flaky.Load() >= 2 }, 10*time.Second, 50*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	require.Equal(t, 1, got["u"], "unique jobs collapsed")
	mu.Unlock()
}

func TestTransactionalEnqueueRollback(t *testing.T) {
	d := pgtest.New(t)
	ctx := context.Background()
	err := db.WithTx(ctx, d.Ent, func(tx *entTx) error {
		require.NoError(t, queue.Enqueue(ctx, tx, "echo", struct{}{}, queue.Opts{}))
		return errors.New("rollback")
	})
	require.Error(t, err)
	n, err := d.Ent.Job.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestTraceparentPropagates(t *testing.T) {
	d := pgtest.New(t)
	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("t").Start(context.Background(), "producer")
	want := span.SpanContext().TraceID().String()
	span.End()
	gotTrace := make(chan string, 1)
	start(t, d, func(q *queue.Queue) {
		q.Register("traced", func(ctx context.Context, j *queue.Job) error {
			gotTrace <- traceID(ctx)
			return nil
		}, queue.HandlerOpts{})
	})
	require.NoError(t, queue.Enqueue(ctx, d.Ent, "traced", struct{}{}, queue.Opts{}))
	select {
	case got := <-gotTrace:
		require.Equal(t, want, got)
	case <-time.After(5 * time.Second):
		t.Fatal("job not processed")
	}
}

func TestNoDoubleProcessingAcrossReplicas(t *testing.T) {
	d := pgtest.New(t)
	ctx := context.Background()
	var count atomic.Int32
	seen := sync.Map{}
	var dup atomic.Int32
	h := func(ctx context.Context, j *queue.Job) error {
		if _, loaded := seen.LoadOrStore(j.ID, true); loaded {
			dup.Add(1)
		}
		count.Add(1)
		return nil
	}
	for i := 0; i < 3; i++ {
		start(t, d, func(q *queue.Queue) { q.Register("work", h, queue.HandlerOpts{Concurrency: 4}) })
	}
	for i := 0; i < 60; i++ {
		require.NoError(t, queue.Enqueue(ctx, d.Ent, "work", map[string]int{"i": i}, queue.Opts{}))
	}
	require.Eventually(t, func() bool { return count.Load() == 60 }, 10*time.Second, 20*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(60), count.Load())
	require.Zero(t, dup.Load())
}
