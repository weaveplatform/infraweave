package cron_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

var quiet = slog.New(slog.DiscardHandler)

func task(name string) queue.Kind {
	return queue.Kind{Name: name, Queue: queue.Maintenance, Policy: queue.Ignore}
}

// replica is one process's view of a database: its pool, its periodic
// tasks and its ticker.
type replica struct {
	pool   *pgxpool.Pool
	tasks  *cron.PeriodicTasks
	ticker *cron.Ticker
	fired  *atomic.Int64
}

// counting is a Source that adds up how many rows its source fired.
type counting struct {
	cron.Source
	n *atomic.Int64
}

func (c counting) Fire(ctx context.Context, limit int) (int, error) {
	n, err := c.Source.Fire(ctx, limit)
	c.n.Add(int64(n))
	return n, err
}

func newReplica(t *testing.T, dsn string, fired *atomic.Int64) replica {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tasks := cron.NewPeriodicTasks(pool, queue.New(pool))
	return replica{
		pool:   pool,
		tasks:  tasks,
		ticker: cron.NewTicker(pool, time.Hour, quiet, counting{tasks, fired}),
		fired:  fired,
	}
}

func ensure(t *testing.T, r replica, k queue.Kind, every time.Duration) {
	t.Helper()
	if err := r.tasks.Ensure(t.Context(), k, every); err != nil {
		t.Fatalf("Ensure(%s): %v", k.Name, err)
	}
}

func scalar[T any](t *testing.T, pool *pgxpool.Pool, query string, args ...any) T {
	t.Helper()
	var v T
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func TestAccTickerTwoReplicas(t *testing.T) {
	dsn := sqltest.DSN(t)
	var fired atomic.Int64
	a, b := newReplica(t, dsn, &fired), newReplica(t, dsn, &fired)
	// More tasks than a batch, so each replica's tick runs several
	// transactions while the other's run beside them.
	const tasks = 2*cron.Batch + 50
	for i := range tasks {
		k := task(fmt.Sprintf("test.task%03d", i))
		ensure(t, a, k, time.Hour)
		ensure(t, b, k, time.Hour)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for _, r := range []replica{a, b, a, b, a, b} {
		wg.Go(func() { errs <- r.ticker.Tick(t.Context()) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := fired.Load(); n != tasks {
		t.Errorf("fired %d rows, want %d: one per task", n, tasks)
	}
	if n := scalar[int64](t, a.pool, `SELECT count(*) FROM jobs`); n != tasks {
		t.Errorf("%d jobs, want %d", n, tasks)
	}
	if n := scalar[int64](t, a.pool, `
SELECT count(*) FROM jobs j JOIN periodic_tasks p ON p.name = j.kind
WHERE j.unique_key = p.name AND j.queue = 'maintenance' AND j.state = 'pending'`); n != tasks {
		t.Errorf("%d jobs keyed by their task on the maintenance queue, want %d", n, tasks)
	}
	if n := scalar[int64](
		t,
		a.pool,
		`SELECT count(*) FROM periodic_tasks WHERE next_run_at <= now()`,
	); n != 0 {
		t.Errorf("%d tasks still due", n)
	}
}

func TestAccTickerMissedFirings(t *testing.T) {
	var fired atomic.Int64
	r := newReplica(t, sqltest.DSN(t), &fired)
	k := task("test.minutely")
	ensure(t, r, k, time.Minute)
	// Ten and a half intervals missed, as after downtime.
	if _, err := r.pool.Exec(t.Context(),
		`UPDATE periodic_tasks SET next_run_at = now() - interval '630 seconds'`); err != nil {
		t.Fatal(err)
	}
	if err := r.ticker.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := fired.Load(); n != 1 {
		t.Errorf("fired %d times, want once", n)
	}
	// The first future time on the old grid: eleven intervals on, 30
	// seconds from the tick.
	ahead := scalar[float64](
		t,
		r.pool,
		`SELECT extract(epoch FROM next_run_at - now()) FROM periodic_tasks`,
	)
	if ahead <= 0 || ahead > 30 {
		t.Errorf("next run %.1fs from now, want within (0, 30]", ahead)
	}
	if err := r.ticker.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := fired.Load(); n != 1 {
		t.Errorf("fired %d times after a second tick, want once", n)
	}
	// Ignore keeps one job per task while one is pending, whatever fires.
	if _, err := r.pool.Exec(t.Context(),
		`UPDATE periodic_tasks SET next_run_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err := r.ticker.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := scalar[int64](t, r.pool, `SELECT count(*) FROM jobs`); n != 1 {
		t.Errorf("%d jobs, want 1 while the first is pending", n)
	}
}

func TestAccPeriodicTasksEnsure(t *testing.T) {
	var fired atomic.Int64
	r := newReplica(t, sqltest.DSN(t), &fired)
	k := task("test.prune")
	read := func() (time.Duration, float64) {
		t.Helper()
		var (
			micros int64
			ahead  float64
		)
		err := r.pool.QueryRow(t.Context(), `
SELECT extract(epoch FROM interval)::bigint * 1000000, extract(epoch FROM next_run_at - now())
FROM periodic_tasks WHERE name = $1`, k.Name).Scan(&micros, &ahead)
		if err != nil {
			t.Fatal(err)
		}
		return time.Duration(micros) * time.Microsecond, ahead
	}

	ensure(t, r, k, 10*time.Minute)
	if every, ahead := read(); every != 10*time.Minute || ahead > 0 {
		t.Errorf("new task: every %s, next run %.1fs ahead; want 10m0s and due now", every, ahead)
	}
	if _, err := r.pool.Exec(t.Context(),
		`UPDATE periodic_tasks SET next_run_at = now() + interval '5 minutes'`); err != nil {
		t.Fatal(err)
	}
	ensure(t, r, k, time.Hour)
	if every, ahead := read(); every != time.Hour || ahead < 290 || ahead > 300 {
		t.Errorf(
			"longer interval: every %s, next run %.1fs ahead; want 1h0m0s and the next run kept",
			every,
			ahead,
		)
	}
	ensure(t, r, k, time.Minute)
	if every, ahead := read(); every != time.Minute || ahead < 50 || ahead > 60 {
		t.Errorf("shorter interval: every %s, next run %.1fs ahead; want 1m0s and at most a minute",
			every, ahead)
	}

	for _, bad := range []struct {
		k     queue.Kind
		every time.Duration
	}{
		{queue.Kind{Name: "test.coalesce", Queue: queue.Maintenance, Policy: queue.Coalesce}, time.Hour},
		{task("test.never"), 0},
	} {
		if err := r.tasks.Ensure(t.Context(), bad.k, bad.every); !errors.Is(err, cron.ErrTask) {
			t.Errorf("Ensure(%s, %s) = %v, want ErrTask", bad.k.Name, bad.every, err)
		}
	}
	if n := scalar[int64](t, r.pool, `SELECT count(*) FROM periodic_tasks`); n != 1 {
		t.Errorf("%d rows, want only the valid task's", n)
	}
}

func TestAccTickerOnlyRegisteredTasks(t *testing.T) {
	var fired atomic.Int64
	dsn := sqltest.DSN(t)
	newer, older := newReplica(t, dsn, &fired), newReplica(t, dsn, &fired)
	if err := older.ticker.Tick(t.Context()); err != nil || fired.Load() != 0 {
		t.Fatalf("tick with no tasks: fired %d, %v", fired.Load(), err)
	}
	ensure(t, newer, task("test.new"), time.Hour)
	if err := older.ticker.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := fired.Load(); n != 0 {
		t.Errorf("a replica that does not know the task fired it %d times", n)
	}
	if err := newer.ticker.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := fired.Load(); n != 1 {
		t.Errorf("the replica that knows the task fired it %d times, want once", n)
	}
}

// script is a Source whose Fire returns the next of its errors, one per
// call, and signals each call.
type script struct {
	errs  []error
	calls chan int
	n     int
}

func (*script) Name() string { return "script" }

func (s *script) Fire(ctx context.Context, _ int) (int, error) {
	var err error
	if s.n < len(s.errs) {
		err = s.errs[s.n]
	}
	s.n++
	select {
	case s.calls <- s.n:
	case <-ctx.Done():
	}
	return 0, err
}

func TestAccTickerRun(t *testing.T) {
	pool := sqltest.Open(t).Primary()
	down := fmt.Errorf("down: %w", kernel.ErrUnavailable)
	// The fifth call shows that the fourth's warning is already logged.
	s := &script{errs: []error{down, down, nil, down, down}, calls: make(chan int)}
	var out logs
	ticker := cron.NewTicker(pool, time.Millisecond, slog.New(slog.NewTextHandler(&out, nil)), s)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		ticker.Run(ctx)
		close(done)
	}()
	for n := range s.calls {
		if n == len(s.errs) {
			break
		}
	}
	cancel()
	<-done
	got := out.String()
	for line, want := range map[string]int{
		"scheduler ticker started":                        1,
		`scheduler tick failed" error="fire script: down`: 2,
		"scheduler tick recovered":                        1,
		"scheduler ticker stopped":                        1,
	} {
		if n := strings.Count(got, line); n != want {
			t.Errorf("%q logged %d times, want %d:\n%s", line, n, want, got)
		}
	}
}

func TestUnitPeriodicTasksUnreachable(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://iw:iw@127.0.0.1:1/iw?connect_timeout=5")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tasks := cron.NewPeriodicTasks(pool, queue.New(pool))
	if err := tasks.Ensure(
		t.Context(),
		task("test.prune"),
		time.Hour,
	); !errors.Is(
		err,
		kernel.ErrUnavailable,
	) {
		t.Errorf("Ensure: %v, want kernel.ErrUnavailable", err)
	}
	ticker := cron.NewTicker(pool, time.Hour, quiet, tasks)
	if err := ticker.Tick(t.Context()); err == nil {
		t.Error("Tick: nil, want the transaction's error")
	}
}

type logs struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
