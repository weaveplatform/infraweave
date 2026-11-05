package queuetest

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// fast is a configuration whose waits suit a test: queues poll often and
// leases are short enough to watch lapse.
func fast() queue.Config {
	c := queue.Defaults()
	c.PollInterval = 20 * time.Millisecond
	c.Lease = 3 * time.Second
	c.ShutdownGrace = 5 * time.Second
	return c
}

// logs is a concurrency-safe buffer for the workers' JSON logs.
type logs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type harness struct {
	q    *queue.Queue
	pool *pgxpool.Pool
	logs *logs
	stop func()
}

// start runs reg's handlers with cfg on a database of its own, woken by a
// real listener unless quiet, until stop or the test's end.
func start(
	t *testing.T,
	cfg queue.Config,
	reg *queue.Registry,
	ent queue.Entitlements,
	quiet bool,
) *harness {
	t.Helper()
	db := sqltest.Open(t)
	h := &harness{q: queue.New(db.Primary()), pool: db.Primary(), logs: &logs{}}
	log := slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var wake <-chan notify.Message
	if !quiet {
		l := notify.NewListener(db.ConnConfig, log)
		sub, err := l.Subscribe(notify.Jobs)
		if err != nil {
			t.Fatal(err)
		}
		wake = sub.C
		wg.Go(func() { l.Run(ctx) })
	}
	w := queue.NewWorkers(h.q, reg, cfg, ent, wake, log, "test-replica")
	wg.Go(func() { w.Run(ctx) })
	var once sync.Once
	h.stop = func() {
		once.Do(func() {
			cancel()
			wg.Wait()
		})
	}
	t.Cleanup(h.stop)
	return h
}

// eventually fails the test unless cond holds within 15 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

func state(t *testing.T, pool *pgxpool.Pool, id int64) func() bool {
	t.Helper()
	return func() bool {
		r := read(t, pool, id)
		return r.State != "pending" && r.State != "running"
	}
}

func register(
	t *testing.T,
	r *queue.Registry,
	k queue.Kind,
	h queue.Handler,
	o queue.HandlerOptions,
) {
	t.Helper()
	if err := r.Register(k, h, o); err != nil {
		t.Fatal(err)
	}
}

func kind(name string, attempts int) queue.Kind {
	return queue.Kind{Name: name, Queue: queue.Default, Policy: queue.Ignore, MaxAttempts: attempts}
}

// TestAccWorkersOutcomes checks that each handler result ends its job as
// decision 0011's outcome table says.
func TestAccWorkersOutcomes(t *testing.T) {
	r := queue.NewRegistry()
	var retried atomic.Int32
	noOpts := queue.HandlerOptions{}
	register(t, r, kind("t.ok", 0), func(context.Context, queue.Job) error { return nil }, noOpts)
	register(t, r, kind("t.snooze", 0), func(context.Context, queue.Job) error {
		return queue.Snooze(time.Hour)
	}, noOpts)
	register(t, r, kind("t.permanent", 5), func(context.Context, queue.Job) error {
		return queue.Permanent(errors.New("bad payload"))
	}, noOpts)
	register(
		t,
		r,
		kind("t.fail", 1),
		func(context.Context, queue.Job) error { return errors.New("boom") },
		noOpts,
	)
	register(
		t,
		r,
		kind("t.panic", 1),
		func(context.Context, queue.Job) error { panic("kaboom") },
		noOpts,
	)
	register(t, r, kind("t.slow", 1), func(ctx context.Context, _ queue.Job) error {
		<-ctx.Done()
		return ctx.Err()
	}, queue.HandlerOptions{Timeout: 100 * time.Millisecond})
	register(t, r, kind("t.retry", 2), func(context.Context, queue.Job) error {
		if retried.Add(1) == 1 {
			return errors.New("transient")
		}
		return nil
	}, queue.HandlerOptions{RetryDelay: func(int) time.Duration { return 0 }})
	h := start(t, fast(), r, queue.AllowAll{}, false)
	ids := map[string]int64{}
	for n, attempts := range map[string]int{
		"t.ok": 0, "t.snooze": 0, "t.permanent": 5, "t.fail": 1, "t.panic": 1, "t.slow": 1, "t.retry": 2,
	} {
		ids[n] = enqueue(t, h.q, kind(n, attempts), nil)
	}
	for _, n := range []string{"t.ok", "t.permanent", "t.fail", "t.panic", "t.slow", "t.retry"} {
		eventually(t, n+" to finish", state(t, h.pool, ids[n]))
	}
	eventually(
		t,
		"the snooze",
		func() bool { return read(t, h.pool, ids["t.snooze"]).RunAtSeconds > 3000 },
	)
	for n, want := range map[string]struct {
		state, lastError string
		attempt          int
	}{
		"t.ok":        {"succeeded", "", 1},
		"t.snooze":    {"pending", "", 0},
		"t.permanent": {"dead", "bad payload", 1},
		"t.fail":      {"dead", "boom", 1},
		"t.panic":     {"dead", "handler panicked: kaboom", 1},
		"t.slow":      {"dead", "job timed out after 100ms: context deadline exceeded", 1},
		"t.retry":     {"succeeded", "transient", 2},
	} {
		got := read(t, h.pool, ids[n])
		if got.State != want.state || got.LastError != want.lastError ||
			got.Attempt != want.attempt {
			t.Errorf(
				"%s: %s, %q, attempt %d; want %+v",
				n,
				got.State,
				got.LastError,
				got.Attempt,
				want,
			)
		}
	}
	out := h.logs.String()
	for _, line := range []string{
		`"msg":"job handler panicked"`, `"msg":"job dead: permanent failure"`,
		`"msg":"job dead: attempts exhausted"`, `"msg":"queue workers started"`,
	} {
		if !strings.Contains(out, line) {
			t.Errorf("no %s in the logs", line)
		}
	}
}

// TestAccWorkersCompleteTx checks that a handler completing its job in its
// own transaction is not completed a second time.
func TestAccWorkersCompleteTx(t *testing.T) {
	r := queue.NewRegistry()
	var h *harness
	register(t, r, kind("t.write", 0), func(ctx context.Context, j queue.Job) error {
		return tx.Do(ctx, h.pool, tx.Options{}, func(ctx context.Context) error {
			if _, err := tx.Q(ctx, h.pool).
				Exec(ctx, "INSERT INTO effects VALUES ($1)", j.ID); err != nil {
				return err
			}
			return h.q.CompleteTx(ctx, j)
		})
	}, queue.HandlerOptions{})
	h = start(t, fast(), r, queue.AllowAll{}, false)
	exec(t, h.pool, "CREATE TABLE effects (job bigint PRIMARY KEY)")
	id := enqueue(t, h.q, kind("t.write", 0), nil)
	eventually(t, "the job", state(t, h.pool, id))
	eventually(
		t,
		"its outcome",
		func() bool { return strings.Contains(h.logs.String(), `"outcome":"succeeded"`) },
	)
	if out := h.logs.String(); strings.Contains(out, "lease lost") ||
		strings.Contains(out, "not recorded") {
		t.Errorf("a completed job was completed again: %s", out)
	}
}

// TestAccWorkersLeaseExtension checks that a handler running past its
// lease keeps its job: without renewal the job would be recovered and run
// again.
func TestAccWorkersLeaseExtension(t *testing.T) {
	cfg := fast()
	cfg.Lease = 300 * time.Millisecond
	r := queue.NewRegistry()
	var runs atomic.Int32
	register(t, r, kind("t.long", 0), func(ctx context.Context, _ queue.Job) error {
		runs.Add(1)
		select {
		case <-time.After(time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, queue.HandlerOptions{})
	h := start(t, cfg, r, queue.AllowAll{}, false)
	id := enqueue(t, h.q, kind("t.long", 0), nil)
	eventually(t, "the job", state(t, h.pool, id))
	if r := read(t, h.pool, id); r.State != "succeeded" || r.Generation != 1 || runs.Load() != 1 {
		t.Errorf("%+v after %d runs; want one run of one claim", r, runs.Load())
	}
}

// TestAccWorkersLeaseLost checks that a handler whose job was taken over
// sees its context end with ErrLeaseLost and records nothing.
func TestAccWorkersLeaseLost(t *testing.T) {
	cfg := fast()
	cfg.Lease = 300 * time.Millisecond
	r := queue.NewRegistry()
	started := make(chan struct{}, 2)
	causes := make(chan error, 2)
	register(t, r, kind("t.taken", 0), func(ctx context.Context, _ queue.Job) error {
		started <- struct{}{}
		<-ctx.Done()
		causes <- context.Cause(ctx)
		return ctx.Err()
	}, queue.HandlerOptions{})
	h := start(t, cfg, r, queue.AllowAll{}, false)
	id := enqueue(t, h.q, kind("t.taken", 0), nil)
	<-started
	// Another replica's claim: the generation moves on.
	exec(t, h.pool, "UPDATE jobs SET generation = generation + 1 WHERE id = $1", id)
	if cause := <-causes; !errors.Is(cause, queue.ErrLeaseLost) {
		t.Errorf("the handler's context ended with %v", cause)
	}
	eventually(t, "the lost lease's log", func() bool {
		return strings.Contains(h.logs.String(), "job lease lost; its result is discarded")
	})
}

func TestAccWorkersUnregisteredKindAndSkipped(t *testing.T) {
	r := queue.NewRegistry()
	var ran atomic.Int32
	gated := queue.Kind{Name: "t.gated", Queue: queue.Default, Policy: queue.Ignore}
	register(t, r, gated, func(context.Context, queue.Job) error {
		ran.Add(1)
		return nil
	}, queue.HandlerOptions{Feature: "webhooks"})
	h := start(t, fast(), r, deny("webhooks"), false)
	unknown := enqueue(t, h.q, kind("t.newer", 0), nil)
	skipped := enqueue(t, h.q, gated, nil, queue.Account(kernel.NewULID()))
	installation := enqueue(t, h.q, gated, nil)
	eventually(t, "the skipped job", state(t, h.pool, skipped))
	eventually(t, "the installation job", state(t, h.pool, installation))
	if read(t, h.pool, skipped).State != "succeeded" || ran.Load() != 1 {
		t.Errorf("skipped job %+v; handler ran %d times, want only for the installation job",
			read(t, h.pool, skipped), ran.Load())
	}
	if !strings.Contains(h.logs.String(), "job skipped") {
		t.Error("the skip was not logged")
	}
	if r := read(t, h.pool, unknown); r.State != "pending" || r.Attempt != 0 {
		t.Errorf("a kind no handler is registered for: %+v", r)
	}
}

type deny string

func (d deny) Allowed(_ context.Context, _ kernel.ULID, feature string) (bool, error) {
	return feature != string(d), nil
}

// TestAccWorkersShutdown checks that a handler finishing within the grace
// completes its job and one that does not is handed back uncounted.
func TestAccWorkersShutdown(t *testing.T) {
	cfg := fast()
	cfg.ShutdownGrace = 300 * time.Millisecond
	r := queue.NewRegistry()
	started := make(chan struct{}, 2)
	finish := make(chan struct{})
	register(t, r, kind("t.finishes", 0), func(context.Context, queue.Job) error {
		started <- struct{}{}
		<-finish
		return nil
	}, queue.HandlerOptions{})
	register(t, r, kind("t.stuck", 0), func(ctx context.Context, _ queue.Job) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}, queue.HandlerOptions{})
	h := start(t, cfg, r, queue.AllowAll{}, false)
	finishes := enqueue(t, h.q, kind("t.finishes", 0), nil)
	stuck := enqueue(t, h.q, kind("t.stuck", 0), nil)
	<-started
	<-started
	done := make(chan struct{})
	go func() {
		h.stop()
		close(done)
	}()
	close(finish)
	<-done
	if r := read(t, h.pool, finishes); r.State != "succeeded" {
		t.Errorf("the handler that finished in the grace: %+v", r)
	}
	if r := read(t, h.pool, stuck); r.State != "pending" || r.Attempt != 0 || !r.Due {
		t.Errorf("the handler cancelled after the grace: %+v; handed back, uncounted", r)
	}
	if !strings.Contains(h.logs.String(), `"outcome":"released"`) {
		t.Error("the release was not logged")
	}
}

// TestAccWorkersQueueIsolation checks that a busy queue never delays
// another's jobs.
func TestAccWorkersQueueIsolation(t *testing.T) {
	cfg := fast()
	cfg.Queues.Default.Workers = 1
	cfg.ShutdownGrace = 0
	r := queue.NewRegistry()
	register(t, r, kind("t.block", 0), func(ctx context.Context, _ queue.Job) error {
		<-ctx.Done()
		return ctx.Err()
	}, queue.HandlerOptions{})
	events := queue.Kind{Name: "t.event", Queue: queue.Events, Policy: queue.Ignore}
	register(
		t,
		r,
		events,
		func(context.Context, queue.Job) error { return nil },
		queue.HandlerOptions{},
	)
	h := start(t, cfg, r, queue.AllowAll{}, false)
	enqueue(t, h.q, kind("t.block", 0), nil)
	waiting := enqueue(t, h.q, kind("t.block", 0), nil)
	other := enqueue(t, h.q, events, nil)
	eventually(t, "the events job", state(t, h.pool, other))
	if r := read(t, h.pool, waiting); r.State != "pending" {
		t.Errorf("default's second job with one worker busy: %+v", r)
	}
}

// TestAccWorkersFallbackPoll checks that jobs are claimed with no
// notifications at all.
func TestAccWorkersFallbackPoll(t *testing.T) {
	r := queue.NewRegistry()
	register(
		t,
		r,
		kind("t.ok", 0),
		func(context.Context, queue.Job) error { return nil },
		queue.HandlerOptions{},
	)
	h := start(t, fast(), r, queue.AllowAll{}, true)
	id := enqueue(t, h.q, kind("t.ok", 0), nil)
	eventually(t, "the job", state(t, h.pool, id))
}

// TestAccWorkersRecoverLapsedLeases checks that the workers take over a job
// whose claimant stopped renewing it.
func TestAccWorkersRecoverLapsedLeases(t *testing.T) {
	r := queue.NewRegistry()
	register(
		t,
		r,
		kind("t.ok", 0),
		func(context.Context, queue.Job) error { return nil },
		queue.HandlerOptions{},
	)
	h := start(t, fast(), r, queue.AllowAll{}, false)
	exec(
		t,
		h.pool,
		`INSERT INTO jobs (queue, kind, payload, priority, run_at, state, attempt, max_attempts,
    generation, lease_until, lease_owner)
VALUES ('default', 't.ok', 'null', 5, now(), 'running', 1, 5, 1, now() - interval '1 second', 'gone')`,
	)
	var id int64
	if err := h.pool.QueryRow(t.Context(), "SELECT id FROM jobs").Scan(&id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the crashed replica's job", state(t, h.pool, id))
	if r := read(t, h.pool, id); r.State != "succeeded" || r.Attempt != 2 || r.Generation != 2 {
		t.Errorf("%+v", r)
	}
}

type failingEntitlements struct{}

func (failingEntitlements) Allowed(context.Context, kernel.ULID, string) (bool, error) {
	return false, errors.New("catalogue unreachable")
}

// TestAccWorkersEntitlementError checks that a job whose entitlement could
// not be read fails its attempt rather than running or being skipped.
func TestAccWorkersEntitlementError(t *testing.T) {
	r := queue.NewRegistry()
	gated := queue.Kind{Name: "t.gated", Queue: queue.Default, Policy: queue.Ignore, MaxAttempts: 1}
	register(t, r, gated, func(context.Context, queue.Job) error {
		t.Error("ran without an entitlement")
		return nil
	}, queue.HandlerOptions{Feature: "webhooks"})
	h := start(t, fast(), r, failingEntitlements{}, false)
	id := enqueue(t, h.q, gated, nil, queue.Account(kernel.NewULID()))
	eventually(t, "the job", state(t, h.pool, id))
	if r := read(
		t,
		h.pool,
		id,
	); r.State != "dead" ||
		r.LastError != "check entitlement: catalogue unreachable" {
		t.Errorf("%+v", r)
	}
}
