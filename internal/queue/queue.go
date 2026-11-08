// Package queue is a PostgreSQL-backed job queue (SELECT ... FOR UPDATE SKIP LOCKED).
// Jobs can be enqueued inside the same ent transaction as the change that triggers them,
// carry the W3C traceparent of their producer, and wake workers on every replica via NOTIFY.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/pubsub"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// Execer is implemented by *ent.Client and *ent.Tx (sql/execquery feature) and *sql.DB.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type Opts struct {
	// RunAt delays the job. Zero = now.
	RunAt time.Time
	// Delay is added to now when RunAt is zero.
	Delay time.Duration
	// UniqueKey collapses jobs: at most one pending job per key (later enqueues are dropped).
	UniqueKey   string
	MaxAttempts int
}

// Enqueue inserts a job using exec (a transaction for atomic enqueue).
func Enqueue(ctx context.Context, exec Execer, kind string, payload any, o Opts) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", kind, err)
	}
	runAt := o.RunAt
	if runAt.IsZero() {
		runAt = time.Now().Add(o.Delay)
	}
	maxAttempts := o.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	var key any
	if o.UniqueKey != "" {
		key = o.UniqueKey
	}
	_, err = exec.ExecContext(ctx, `INSERT INTO jobs
		(kind, payload, status, attempts, max_attempts, run_at, locked_by, last_error, unique_key, traceparent, created_at, updated_at)
		VALUES ($1, $2, 'pending', 0, $3, $4, '', '', $5, $6, now(), now())
		ON CONFLICT DO NOTHING`,
		kind, b, maxAttempts, runAt, key, telemetry.Traceparent(ctx))
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return nil
}

// Job is a dequeued job.
type Job struct {
	ID          int64
	Kind        string
	Payload     json.RawMessage
	Attempt     int
	MaxAttempts int
	traceparent string
}

// Decode unmarshals the payload.
func (j *Job) Decode(v any) error { return json.Unmarshal(j.Payload, v) }

// Handler processes a job. Return nil on success, an error to retry with backoff,
// Snooze to retry later without consuming an attempt, or Permanent to fail without retry.
type Handler func(ctx context.Context, job *Job) error

type snoozeError struct{ after time.Duration }

func (e *snoozeError) Error() string { return fmt.Sprintf("snoozed for %s", e.after) }

// Snooze reschedules the job after d without counting an attempt.
func Snooze(d time.Duration) error { return &snoozeError{after: d} }

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks an error as non-retryable.
func Permanent(err error) error { return &permanentError{err: err} }

type HandlerOpts struct {
	Concurrency int           // per replica, default 2
	Timeout     time.Duration // default 2m
}

type registration struct {
	h    Handler
	opts HandlerOpts
	sem  chan struct{}
}

type Queue struct {
	pool         *pgxpool.Pool
	log          *slog.Logger
	listener     *pubsub.Listener
	workerID     string
	pollInterval time.Duration

	mu       sync.Mutex
	handlers map[string]*registration

	processed metric.Int64Counter
	duration  metric.Float64Histogram
	wg        sync.WaitGroup
}

func New(pool *pgxpool.Pool, listener *pubsub.Listener, log *slog.Logger, pollInterval time.Duration) *Queue {
	host, _ := os.Hostname()
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	meter := otel.Meter(telemetry.ScopeName + "/queue")
	processed, _ := meter.Int64Counter("infra_monitor_jobs_processed", metric.WithDescription("Jobs processed by kind and outcome"))
	duration, _ := meter.Float64Histogram("infra_monitor_job_duration_seconds", metric.WithUnit("s"))
	q := &Queue{
		pool: pool, log: log, listener: listener, pollInterval: pollInterval,
		workerID:  fmt.Sprintf("%s-%d", host, os.Getpid()),
		handlers:  map[string]*registration{},
		processed: processed, duration: duration,
	}
	_, _ = meter.Int64ObservableGauge("infra_monitor_jobs_pending", metric.WithDescription("Pending jobs by kind"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			rows, err := pool.Query(ctx, "SELECT kind, count(*) FROM jobs WHERE status = 'pending' GROUP BY kind")
			if err != nil {
				return nil
			}
			defer rows.Close()
			for rows.Next() {
				var k string
				var n int64
				if rows.Scan(&k, &n) == nil {
					o.Observe(n, metric.WithAttributes(attribute.String("kind", k)))
				}
			}
			return nil
		}))
	return q
}

// Register adds a handler. Must be called before Run.
func (q *Queue) Register(kind string, h Handler, o HandlerOpts) {
	if o.Concurrency <= 0 {
		o.Concurrency = 2
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[kind] = &registration{h: h, opts: o, sem: make(chan struct{}, o.Concurrency)}
}

// Run dispatches jobs until ctx is canceled, then waits for running jobs.
func (q *Queue) Run(ctx context.Context) error {
	var wake <-chan string
	if q.listener != nil {
		ch, unsub := q.listener.Subscribe(pubsub.ChannelJobs, 64)
		defer unsub()
		wake = ch
	}
	ticker := time.NewTicker(q.pollInterval)
	defer ticker.Stop()
	reap := time.NewTicker(time.Minute)
	defer reap.Stop()
	q.dispatch(ctx)
	for {
		select {
		case <-ctx.Done():
			q.wg.Wait()
			return nil
		case <-wake:
			q.dispatch(ctx)
		case <-ticker.C:
			q.dispatch(ctx)
		case <-reap.C:
			if err := q.Reap(ctx); err != nil && ctx.Err() == nil {
				q.log.Warn("job reaper failed", "error", err)
			}
		}
	}
}

func (q *Queue) dispatch(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	rows, err := q.pool.Query(ctx, "SELECT DISTINCT kind FROM jobs WHERE status = 'pending' AND run_at <= now()")
	if err != nil {
		if ctx.Err() == nil {
			q.log.Warn("job poll failed", "error", err)
		}
		return
	}
	kinds, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return
	}
	for _, kind := range kinds {
		q.mu.Lock()
		reg := q.handlers[kind]
		q.mu.Unlock()
		if reg == nil {
			continue
		}
		free := cap(reg.sem) - len(reg.sem)
		if free <= 0 {
			continue
		}
		jobs, err := q.dequeue(ctx, kind, free, reg.opts.Timeout+time.Minute)
		if err != nil {
			if ctx.Err() == nil {
				q.log.Warn("dequeue failed", "kind", kind, "error", err)
			}
			continue
		}
		for _, j := range jobs {
			reg.sem <- struct{}{}
			q.wg.Add(1)
			go func(j *Job) {
				defer q.wg.Done()
				defer func() { <-reg.sem }()
				q.execute(context.WithoutCancel(ctx), reg, j)
			}(j)
		}
		if len(jobs) == free {
			// more may be waiting; poll again soon
			go func() {
				select {
				case <-ctx.Done():
				case <-time.After(50 * time.Millisecond):
					q.dispatch(ctx)
				}
			}()
		}
	}
}

func (q *Queue) dequeue(ctx context.Context, kind string, limit int, lock time.Duration) ([]*Job, error) {
	rows, err := q.pool.Query(ctx, `UPDATE jobs SET status = 'running', attempts = attempts + 1, locked_by = $1,
			locked_until = now() + make_interval(secs => $2), updated_at = now()
		WHERE id IN (
			SELECT id FROM jobs WHERE status = 'pending' AND kind = $3 AND run_at <= now()
			ORDER BY run_at, id LIMIT $4 FOR UPDATE SKIP LOCKED)
		RETURNING id, kind, payload, attempts, max_attempts, traceparent`,
		q.workerID, lock.Seconds(), kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j := &Job{}
		if err := rows.Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempt, &j.MaxAttempts, &j.traceparent); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (q *Queue) execute(ctx context.Context, reg *registration, j *Job) {
	ctx = telemetry.ContextWithTraceparent(ctx, j.traceparent)
	ctx, span := telemetry.Tracer().Start(ctx, "job "+j.Kind, trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("job.kind", j.Kind), attribute.Int64("job.id", j.ID), attribute.Int("job.attempt", j.Attempt)))
	defer span.End()
	start := time.Now()
	jctx, cancel := context.WithTimeout(ctx, reg.opts.Timeout)
	err := safeCall(jctx, reg.h, j)
	cancel()
	outcome := "succeeded"
	var snooze *snoozeError
	var perm *permanentError
	var ferr error
	switch {
	case err == nil:
		_, ferr = q.pool.Exec(ctx, "UPDATE jobs SET status = 'succeeded', finished_at = now(), locked_by = '', locked_until = NULL, updated_at = now() WHERE id = $1", j.ID)
	case errors.As(err, &snooze):
		outcome = "snoozed"
		ferr = q.reschedule(ctx, j.ID, snooze.after, "", true)
	case errors.As(err, &perm) || j.Attempt >= j.MaxAttempts:
		outcome = "dead"
		telemetry.RecordError(span, err)
		q.log.ErrorContext(ctx, "job failed permanently", "kind", j.Kind, "job_id", j.ID, "attempt", j.Attempt, "error", err)
		_, ferr = q.pool.Exec(ctx, "UPDATE jobs SET status = 'dead', finished_at = now(), last_error = $2, locked_by = '', locked_until = NULL, updated_at = now() WHERE id = $1", j.ID, err.Error())
	default:
		outcome = "retry"
		telemetry.RecordError(span, err)
		backoff := time.Duration(math.Min(float64(time.Second)*math.Pow(2, float64(j.Attempt)), float64(10*time.Minute)))
		q.log.WarnContext(ctx, "job failed, will retry", "kind", j.Kind, "job_id", j.ID, "attempt", j.Attempt, "retry_in", backoff, "error", err)
		ferr = q.reschedule(ctx, j.ID, backoff, err.Error(), false)
	}
	if ferr != nil {
		q.log.ErrorContext(ctx, "failed to record job outcome", "job_id", j.ID, "error", ferr)
	}
	attrs := metric.WithAttributes(attribute.String("kind", j.Kind), attribute.String("outcome", outcome))
	q.processed.Add(ctx, 1, attrs)
	q.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attribute.String("kind", j.Kind)))
}

func safeCall(ctx context.Context, h Handler, j *Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return h(ctx, j)
}

// reschedule puts a job back to pending. If another pending job with the same unique key
// exists, this one is superseded and marked failed.
func (q *Queue) reschedule(ctx context.Context, id int64, after time.Duration, lastErr string, keepAttempt bool) error {
	dec := 0
	if keepAttempt {
		dec = 1
	}
	_, err := q.pool.Exec(ctx, `UPDATE jobs SET status = 'pending', run_at = now() + make_interval(secs => $2),
		attempts = attempts - $4, last_error = $3, locked_by = '', locked_until = NULL, updated_at = now() WHERE id = $1`,
		id, after.Seconds(), lastErr, dec)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		_, err = q.pool.Exec(ctx, "UPDATE jobs SET status = 'failed', finished_at = now(), last_error = 'superseded by a newer job', updated_at = now() WHERE id = $1", id)
	}
	return err
}

// Reap returns jobs whose worker died (lock expired) to pending.
func (q *Queue) Reap(ctx context.Context) error {
	rows, err := q.pool.Query(ctx, "SELECT id FROM jobs WHERE status = 'running' AND locked_until < now() FOR UPDATE SKIP LOCKED")
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	for _, id := range ids {
		q.log.Warn("reaping stale job", "job_id", id)
		if err := q.reschedule(ctx, id, 0, "worker lost", false); err != nil {
			return err
		}
	}
	return nil
}

// Cleanup deletes finished jobs older than retention.
func (q *Queue) Cleanup(ctx context.Context, retention time.Duration) (int64, error) {
	tag, err := q.pool.Exec(ctx, "DELETE FROM jobs WHERE status IN ('succeeded','failed','dead') AND finished_at < now() - make_interval(secs => $1)", retention.Seconds())
	return tag.RowsAffected(), err
}

// Drain runs pending jobs synchronously, one at a time in run_at order, until none are
// left (tests and tools). With includeFuture, delayed jobs run too (still in order).
func (q *Queue) Drain(ctx context.Context, includeFuture bool) (int, error) {
	q.mu.Lock()
	kinds := make([]string, 0, len(q.handlers))
	for k := range q.handlers {
		kinds = append(kinds, k)
	}
	q.mu.Unlock()
	for n := 0; n < 10000; n++ {
		rows, err := q.pool.Query(ctx, `UPDATE jobs SET status = 'running', attempts = attempts + 1, locked_by = $1,
				locked_until = now() + interval '1 hour', updated_at = now()
			WHERE id = (SELECT id FROM jobs WHERE status = 'pending' AND kind = ANY($2) AND ($3 OR run_at <= now())
				ORDER BY run_at, id LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING id, kind, payload, attempts, max_attempts, traceparent`, q.workerID, kinds, includeFuture)
		if err != nil {
			return n, err
		}
		var jobs []*Job
		for rows.Next() {
			j := &Job{}
			if err := rows.Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempt, &j.MaxAttempts, &j.traceparent); err != nil {
				rows.Close()
				return n, err
			}
			jobs = append(jobs, j)
		}
		rows.Close()
		if len(jobs) == 0 {
			return n, nil
		}
		q.mu.Lock()
		reg := q.handlers[jobs[0].Kind]
		q.mu.Unlock()
		q.execute(ctx, reg, jobs[0])
	}
	return 0, fmt.Errorf("drain: too many jobs")
}
