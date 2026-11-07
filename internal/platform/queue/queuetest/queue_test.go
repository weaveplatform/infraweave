package queuetest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

const lease = time.Minute

var (
	work    = queue.Kind{Name: "test.work", Queue: queue.Default, Policy: queue.Ignore}
	refresh = queue.Kind{Name: "test.refresh", Queue: queue.Default, Policy: queue.Coalesce}
	kinds   = []string{work.Name, refresh.Name}
)

func open(t *testing.T) (*queue.Queue, *pgxpool.Pool) {
	t.Helper()
	pool := sqltest.Open(t).Primary()
	return queue.New(pool), pool
}

type row struct {
	State, LastError       string
	Attempt                int
	Generation             int64
	Rerun                  bool
	Payload                json.RawMessage
	Due                    bool
	RunAtSeconds, Finished float64
}

// read is job id's row; RunAtSeconds is run_at less now.
func read(t *testing.T, pool *pgxpool.Pool, id int64) row {
	t.Helper()
	var r row
	err := pool.QueryRow(t.Context(), `
SELECT state, coalesce(last_error, ''), attempt, generation, rerun, payload, run_at <= now(),
    extract(epoch FROM run_at - now()), coalesce(extract(epoch FROM now() - finished_at), -1)
FROM jobs WHERE id = $1`, id).Scan(&r.State, &r.LastError, &r.Attempt, &r.Generation, &r.Rerun,
		&r.Payload, &r.Due, &r.RunAtSeconds, &r.Finished)
	if err != nil {
		t.Fatalf("read job %d: %v", id, err)
	}
	return r
}

func exec(t *testing.T, pool *pgxpool.Pool, stmt string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), stmt, args...); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

func enqueue(t *testing.T, q *queue.Queue, k queue.Kind, payload any, opts ...queue.Option) int64 {
	t.Helper()
	id, err := q.Enqueue(t.Context(), k, payload, opts...)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return id
}

func claim(t *testing.T, q *queue.Queue, n int) []queue.Job {
	t.Helper()
	jobs, err := q.Claim(t.Context(), queue.Default, kinds, n, lease, "test")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return jobs
}

func claimOne(t *testing.T, q *queue.Queue) queue.Job {
	t.Helper()
	jobs := claim(t, q, 1)
	if len(jobs) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(jobs))
	}
	return jobs[0]
}

func ids(jobs []queue.Job) []int64 {
	out := make([]int64, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ID)
	}
	return out
}

// TestAccQueueConcurrentClaims has N claimers drain M jobs: each runs once.
func TestAccQueueConcurrentClaims(t *testing.T) {
	q, pool := open(t)
	const m = 200
	for i := range m {
		enqueue(t, q, work, map[string]int{"i": i})
	}
	var (
		mu   sync.Mutex
		seen = map[int64]int{}
		wg   sync.WaitGroup
	)
	for range 8 {
		wg.Go(func() {
			for {
				jobs, err := q.Claim(t.Context(), queue.Default, kinds, 5, lease, "test")
				if err != nil {
					t.Error(err)
					return
				}
				if len(jobs) == 0 {
					return
				}
				for _, j := range jobs {
					mu.Lock()
					seen[j.ID]++
					mu.Unlock()
					if err := q.Complete(t.Context(), j); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	wg.Wait()
	if len(seen) != m {
		t.Errorf("%d distinct jobs ran, want %d", len(seen), m)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("job %d ran %d times", id, n)
		}
	}
	var succeeded int
	if err := pool.QueryRow(
		t.Context(),
		"SELECT count(*) FROM jobs WHERE state = 'succeeded'",
	).Scan(&succeeded); err != nil || succeeded != m {
		t.Errorf("%d succeeded (%v)", succeeded, err)
	}
}

func TestAccQueuePriority(t *testing.T) {
	q, _ := open(t)
	past := time.Now().Add(-time.Hour)
	later := enqueue(t, q, work, nil, queue.Priority(5))
	first := enqueue(t, q, work, nil, queue.Priority(0))
	older := enqueue(t, q, work, nil, queue.Priority(5), queue.RunAt(past))
	enqueue(t, q, work, nil, queue.RunAt(time.Now().Add(time.Hour)))
	enqueue(t, q, queue.Kind{Name: "test.other", Queue: queue.Default, Policy: queue.Ignore}, nil)
	enqueue(t, q, queue.Kind{Name: "test.work", Queue: queue.Events, Policy: queue.Ignore}, nil)
	got := ids(claim(t, q, 10))
	if want := []int64{first, older, later}; !slices.Equal(got, want) {
		t.Errorf("claimed %v, want %v: priority, then run time, then insertion; "+
			"nothing future, of another kind or of another queue", got, want)
	}
}

// TestAccQueueOrderingKeys checks that jobs sharing a key run one at a
// time in insertion order, while other keys and unkeyed jobs proceed, also
// with several claimers racing.
func TestAccQueueOrderingKeys(t *testing.T) {
	q, _ := open(t)
	a1 := enqueue(t, q, work, nil, queue.OrderingKey("a"))
	a2 := enqueue(t, q, work, nil, queue.OrderingKey("a"), queue.Priority(0))
	b1 := enqueue(t, q, work, nil, queue.OrderingKey("b"))
	free := enqueue(t, q, work, nil)
	got := claim(t, q, 10)
	if !slices.Equal(ids(got), []int64{a1, b1, free}) {
		t.Fatalf("first claim %v, want [%d %d %d]: a2 waits behind a1 despite its priority",
			ids(got), a1, b1, free)
	}
	if more := claim(t, q, 10); len(more) != 0 {
		t.Fatalf("a2 claimed while a1 runs: %v", ids(more))
	}
	if err := q.Complete(t.Context(), got[0]); err != nil {
		t.Fatal(err)
	}
	if next := claimOne(t, q); next.ID != a2 {
		t.Errorf("after a1, claimed %d, want a2 %d", next.ID, a2)
	}

	var order []int64
	var mu sync.Mutex
	want := make([]int64, 0, 30)
	for range 30 {
		want = append(want, enqueue(t, q, work, nil, queue.OrderingKey("c")))
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for {
				jobs, err := q.Claim(t.Context(), queue.Default, kinds, 3, lease, "test")
				if err != nil {
					t.Error(err)
					return
				}
				for _, j := range jobs {
					mu.Lock()
					order = append(order, j.ID)
					mu.Unlock()
					if err := q.Complete(t.Context(), j); err != nil {
						t.Error(err)
					}
				}
				mu.Lock()
				done := len(order) == len(want)
				mu.Unlock()
				if done {
					return
				}
			}
		})
	}
	wg.Wait()
	if !slices.Equal(order, want) {
		t.Errorf("key c ran in order %v, want %v", order, want)
	}
}

func TestAccQueueCoalesce(t *testing.T) {
	q, pool := open(t)
	soon, later := time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
	id := enqueue(t, q, refresh, map[string]int{"n": 1}, queue.UniqueKey("x"), queue.RunAt(soon))
	if again := enqueue(t, q, refresh, map[string]int{"n": 2}, queue.UniqueKey("x"),
		queue.RunAt(later)); again != id {
		t.Fatalf("a pending duplicate made job %d, want %d", again, id)
	}
	r := read(t, pool, id)
	if string(r.Payload) != `{"n": 2}` || r.RunAtSeconds > 3601 || r.RunAtSeconds < 3500 {
		t.Errorf(
			"coalesced job: payload %s, due in %.0fs; want the newer payload, the earlier time",
			r.Payload,
			r.RunAtSeconds,
		)
	}
	enqueue(t, q, refresh, map[string]int{"n": 3}, queue.UniqueKey("x"))
	j := claimOne(t, q)
	if string(j.Payload) != `{"n": 3}` {
		t.Errorf("claimed payload %s", j.Payload)
	}
	if again := enqueue(t, q, refresh, map[string]int{"n": 4}, queue.UniqueKey("x")); again != id {
		t.Fatalf("a running duplicate made job %d, want %d", again, id)
	}
	if !read(t, pool, id).Rerun {
		t.Fatal("a duplicate of a running job did not set rerun")
	}
	if err := q.Complete(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	r = read(t, pool, id)
	if r.State != "pending" || r.Attempt != 0 || r.Rerun || !r.Due {
		t.Fatalf("after the run a duplicate arrived in: %+v", r)
	}
	again := claimOne(t, q)
	if string(again.Payload) != `{"n": 4}` || again.Attempt != 1 {
		t.Errorf("rerun claimed with payload %s, attempt %d", again.Payload, again.Attempt)
	}
	if err := q.Complete(t.Context(), again); err != nil {
		t.Fatal(err)
	}
	if r := read(t, pool, id); r.State != "succeeded" || r.Finished < 0 {
		t.Errorf("after the rerun: %+v", r)
	}
}

func TestAccQueueIgnore(t *testing.T) {
	q, pool := open(t)
	id := enqueue(t, q, work, map[string]int{"n": 1}, queue.UniqueKey("x"))
	if dup := enqueue(t, q, work, map[string]int{"n": 2}, queue.UniqueKey("x")); dup != 0 {
		t.Errorf("a pending duplicate returned %d, want 0", dup)
	}
	j := claimOne(t, q)
	if dup := enqueue(t, q, work, nil, queue.UniqueKey("x")); dup != 0 {
		t.Errorf("a running duplicate returned %d, want 0", dup)
	}
	if r := read(t, pool, id); string(r.Payload) != `{"n": 1}` || r.Rerun {
		t.Errorf("Ignore changed the job: %+v", r)
	}
	if err := q.Complete(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if next := enqueue(t, q, work, nil, queue.UniqueKey("x")); next == 0 || next == id {
		t.Errorf("after the job succeeded, its key enqueued %d", next)
	}
}

// TestAccQueueRolledBackEnqueue checks that a job enqueued in a transaction
// that rolls back neither exists nor wakes a worker, and that a committed
// one wakes its queue's workers.
func TestAccQueueRolledBackEnqueue(t *testing.T) {
	q, pool := open(t)
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(t.Context(), "LISTEN infraweave_jobs"); err != nil {
		t.Fatal(err)
	}
	abandon := errors.New("abandon the change")
	err = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		if _, err := q.EnqueueTx(ctx, work, nil); err != nil {
			return err
		}
		return abandon
	})
	if !errors.Is(err, abandon) {
		t.Fatal(err)
	}
	events := queue.Kind{Name: "test.fanout", Queue: queue.Events, Policy: queue.Ignore}
	err = tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		_, err := q.EnqueueTx(ctx, events, nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// The rolled-back job's notification would arrive first.
	n, err := conn.Conn().WaitForNotification(ctx)
	if err != nil || n.Payload != string(queue.Events) {
		t.Errorf("first notification %+v (%v), want the committed job's queue", n, err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM jobs").
		Scan(&count); err != nil ||
		count != 1 {
		t.Errorf("%d jobs (%v), want only the committed one", count, err)
	}
}

// TestAccQueueCompleteTxAtomic checks that a handler's writes commit with
// its completion, and roll back when its lease was lost.
func TestAccQueueCompleteTxAtomic(t *testing.T) {
	q, pool := open(t)
	exec(t, pool, "CREATE TABLE effects (job bigint PRIMARY KEY)")
	handle := func(j queue.Job) error {
		return tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
			if _, err := tx.Q(ctx, pool).
				Exec(ctx, "INSERT INTO effects VALUES ($1)", j.ID); err != nil {
				return err
			}
			return q.CompleteTx(ctx, j)
		})
	}
	enqueue(t, q, work, nil)
	j := claimOne(t, q)
	if err := handle(j); err != nil {
		t.Fatal(err)
	}

	id := enqueue(t, q, work, nil)
	stale := claimOne(t, q)
	exec(t, pool, "UPDATE jobs SET lease_until = now() - interval '1 second' WHERE id = $1", id)
	if n, err := q.Recover(t.Context()); err != nil || n != 1 {
		t.Fatalf("Recover = %d, %v", n, err)
	}
	fresh := claimOne(t, q)
	if err := handle(stale); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("the stale handler's CompleteTx: %v", err)
	}
	if err := handle(fresh); err != nil {
		t.Fatal(err)
	}
	var effects []int64
	rows, err := pool.Query(t.Context(), "SELECT job FROM effects ORDER BY job")
	if err == nil {
		effects, err = pgx.CollectRows(rows, pgx.RowTo[int64])
	}
	if err != nil || !slices.Equal(effects, []int64{j.ID, id}) {
		t.Errorf("effects %v (%v): each job's write exactly once", effects, err)
	}
}

// TestAccQueueFencing checks that every transition of a job claimed again
// after its lease lapsed is refused to the earlier claim.
func TestAccQueueFencing(t *testing.T) {
	q, pool := open(t)
	id := enqueue(t, q, work, nil)
	stale := claimOne(t, q)
	exec(t, pool, "UPDATE jobs SET lease_until = now() - interval '1 second' WHERE id = $1", id)
	if _, err := q.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh := claimOne(t, q)
	if fresh.Generation != stale.Generation+1 || fresh.Attempt != 2 {
		t.Fatalf("reclaimed at generation %d, attempt %d", fresh.Generation, fresh.Attempt)
	}
	ctx := t.Context()
	cause := errors.New("cause")
	for name, f := range map[string]func() error{
		"Extend":   func() error { return q.Extend(ctx, stale, lease) },
		"Complete": func() error { return q.Complete(ctx, stale) },
		"Fail":     func() error { _, err := q.Fail(ctx, stale, cause); return err },
		"Kill":     func() error { return q.Kill(ctx, stale, cause) },
		"Snooze":   func() error { return q.Snooze(ctx, stale, time.Minute) },
		"Release":  func() error { return q.Release(ctx, stale) },
	} {
		if err := f(); !errors.Is(err, queue.ErrLeaseLost) {
			t.Errorf("%s by the stale claim: %v", name, err)
		}
	}
	if r := read(t, pool, id); r.State != "running" || r.Generation != fresh.Generation {
		t.Errorf("the stale claim changed the job: %+v", r)
	}
	if err := q.Extend(ctx, fresh, lease); err != nil {
		t.Errorf("Extend by the current claim: %v", err)
	}
}

func TestAccQueueRecovery(t *testing.T) {
	q, pool := open(t)
	id := enqueue(t, q, work, nil)
	last := enqueue(t, q, work, nil, queue.MaxAttempts(1))
	claim(t, q, 2)
	if n, err := q.Recover(t.Context()); err != nil || n != 0 {
		t.Fatalf("Recover of live leases = %d, %v", n, err)
	}
	exec(t, pool, "UPDATE jobs SET lease_until = now() - interval '1 second'")
	if n, err := q.Recover(t.Context()); err != nil || n != 2 {
		t.Fatalf("Recover = %d, %v", n, err)
	}
	if r := read(t, pool, id); r.State != "pending" || r.Attempt != 1 || !r.Due {
		t.Errorf("recovered job: %+v; the lapsed attempt counts and it is due at once", r)
	}
	if r := read(
		t,
		pool,
		last,
	); r.State != "dead" || r.LastError != "lease lapsed" ||
		r.Finished < 0 {
		t.Errorf("a job whose last attempt lapsed: %+v", r)
	}
}

func TestAccQueueOutcomes(t *testing.T) {
	q, pool := open(t)
	id := enqueue(t, q, work, nil, queue.MaxAttempts(2))
	j := claimOne(t, q)
	if dead, err := q.Fail(t.Context(), j, errors.New("first failure")); err != nil || dead {
		t.Fatalf("Fail = %v, %v", dead, err)
	}
	r := read(t, pool, id)
	if r.State != "pending" || r.LastError != "first failure" || r.RunAtSeconds < 0.5 ||
		r.RunAtSeconds > 1.1 {
		t.Fatalf("after the first failure: %+v; attempt 1 backs off one second", r)
	}
	exec(t, pool, "UPDATE jobs SET run_at = now() WHERE id = $1", id)
	j = claimOne(t, q)
	if dead, err := q.Fail(t.Context(), j, errors.New("second failure")); err != nil || !dead {
		t.Fatalf("Fail at the limit = %v, %v", dead, err)
	}
	if r := read(
		t,
		pool,
		id,
	); r.State != "dead" || r.LastError != "second failure" ||
		r.Attempt != 2 {
		t.Errorf("after the last attempt: %+v", r)
	}

	id = enqueue(t, q, work, nil)
	if err := q.Kill(t.Context(), claimOne(t, q), errors.New("bad payload")); err != nil {
		t.Fatal(err)
	}
	if r := read(t, pool, id); r.State != "dead" || r.Attempt != 1 {
		t.Errorf("killed: %+v", r)
	}

	id = enqueue(t, q, work, nil)
	if err := q.Snooze(t.Context(), claimOne(t, q), time.Hour); err != nil {
		t.Fatal(err)
	}
	if r := read(t, pool, id); r.State != "pending" || r.Attempt != 0 || r.RunAtSeconds < 3500 {
		t.Errorf("snoozed: %+v; the attempt does not count", r)
	}

	id = enqueue(t, q, work, nil)
	if err := q.Release(t.Context(), claimOne(t, q)); err != nil {
		t.Fatal(err)
	}
	if r := read(t, pool, id); r.State != "pending" || r.Attempt != 0 || !r.Due {
		t.Errorf("released: %+v; due at once, the attempt not counted", r)
	}
}

func TestAccQueuePrune(t *testing.T) {
	q, pool := open(t)
	states := map[string]int64{}
	for _, s := range []string{"old-succeeded", "new-succeeded", "old-cancelled", "old-dead", "new-dead", "pending"} {
		states[s] = enqueue(t, q, work, nil)
	}
	exec(t, pool, `UPDATE jobs SET state = 'succeeded', finished_at = now() - interval '2 hours'
WHERE id = $1`, states["old-succeeded"])
	exec(t, pool, `UPDATE jobs SET state = 'succeeded', finished_at = now() WHERE id = $1`,
		states["new-succeeded"])
	exec(t, pool, `UPDATE jobs SET state = 'cancelled', finished_at = now() - interval '2 hours'
WHERE id = $1`, states["old-cancelled"])
	exec(t, pool, `UPDATE jobs SET state = 'dead', finished_at = now() - interval '31 days'
WHERE id = $1`, states["old-dead"])
	exec(t, pool, `UPDATE jobs SET state = 'dead', finished_at = now() - interval '2 hours'
WHERE id = $1`, states["new-dead"])
	n, err := q.Prune(t.Context(), time.Hour, 30*24*time.Hour)
	if err != nil || n != 3 {
		t.Fatalf("Prune = %d, %v", n, err)
	}
	rows, err := pool.Query(t.Context(), "SELECT id FROM jobs ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	left, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if want := []int64{
		states["new-succeeded"],
		states["new-dead"],
		states["pending"],
	}; err != nil ||
		!slices.Equal(left, want) {
		t.Errorf("left %v (%v), want %v", left, err, want)
	}
}

func TestAccQueueDeadOperations(t *testing.T) {
	q, pool := open(t)
	account := kernel.NewULID()
	dead := make([]int64, 0, 3)
	for i := range 3 {
		id := enqueue(
			t,
			q,
			work,
			map[string]int{"i": i},
			queue.Account(account),
			queue.UniqueKey(string(rune('a'+i))),
		)
		if err := q.Kill(t.Context(), claimOne(t, q), errors.New("broken")); err != nil {
			t.Fatal(err)
		}
		dead = append(dead, id)
	}
	page, err := q.ListDead(t.Context(), 0, 2)
	if err != nil || len(page) != 2 || page[0].ID != dead[0] || page[1].ID != dead[1] {
		t.Fatalf("first page %+v (%v)", page, err)
	}
	d := page[0]
	if d.Kind != work.Name || d.Queue != queue.Default || d.Account != account ||
		d.UniqueKey != "a" ||
		d.Attempt != 1 ||
		d.LastError != "broken" ||
		d.FinishedAt.Before(d.CreatedAt) ||
		string(d.Payload) != `{"i": 0}` {
		t.Errorf("dead job %+v", d)
	}
	if rest, err := q.ListDead(
		t.Context(),
		page[1].ID,
		2,
	); err != nil || len(rest) != 1 ||
		rest[0].ID != dead[2] {
		t.Errorf("second page %+v (%v)", rest, err)
	}

	if one, err := q.ReadDead(t.Context(), dead[0]); err != nil || one.ID != d.ID ||
		one.LastError != d.LastError || string(one.Payload) != string(d.Payload) {
		t.Errorf("ReadDead = %+v, %v; want %+v", one, err, d)
	}
	if err := q.Retry(t.Context(), dead[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ReadDead(t.Context(), dead[0]); !errors.Is(err, queue.ErrNotDead) {
		t.Errorf("reading a retried job as dead: %v", err)
	}
	if r := read(
		t,
		pool,
		dead[0],
	); r.State != "pending" || r.Attempt != 0 || !r.Due ||
		r.Finished >= 0 {
		t.Errorf("retried: %+v", r)
	}
	if err := q.Retry(t.Context(), dead[0]); !errors.Is(err, queue.ErrNotDead) {
		t.Errorf("retry of a pending job: %v", err)
	}
	enqueue(t, q, work, nil, queue.UniqueKey("b"))
	if err := q.Retry(t.Context(), dead[1]); !errors.Is(err, queue.ErrDuplicate) {
		t.Errorf("retry while its key is pending: %v", err)
	}
	if err := q.Discard(t.Context(), dead[2]); err != nil {
		t.Fatal(err)
	}
	if r := read(t, pool, dead[2]); r.State != "cancelled" {
		t.Errorf("discarded: %+v", r)
	}
	if err := q.Discard(t.Context(), dead[2]); !errors.Is(err, queue.ErrNotDead) {
		t.Errorf("second discard: %v", err)
	}
}

func TestAccQueueStats(t *testing.T) {
	q, _ := open(t)
	enqueue(t, q, work, nil)
	enqueue(t, q, work, nil)
	enqueue(t, q, work, nil, queue.RunAt(time.Now().Add(time.Hour)))
	enqueue(
		t,
		q,
		queue.Kind{Name: "test.prune", Queue: queue.Maintenance, Policy: queue.Ignore},
		nil,
	)
	claimOne(t, q)
	stats, err := q.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != len(queue.Queues) {
		t.Fatalf("%d queues reported", len(stats))
	}
	want := map[queue.Name]queue.Stat{
		queue.Default:     {Queue: queue.Default, Visible: 1, InFlight: 1},
		queue.Maintenance: {Queue: queue.Maintenance, Visible: 1},
	}
	for i, s := range stats {
		w, ok := want[s.Queue]
		if !ok {
			w = queue.Stat{Queue: s.Queue}
		}
		if s.Queue != queue.Queues[i] || s != w {
			t.Errorf("stat %d: %+v, want %+v", i, s, w)
		}
	}
}

func TestAccQueuePayloadAndAccount(t *testing.T) {
	q, _ := open(t)
	account := kernel.NewULID()
	id := enqueue(t, q, work, map[string]string{"stack": "s1"}, queue.Account(account))
	j := claimOne(t, q)
	if j.ID != id || j.Account != account || j.Kind != work.Name || j.Queue != queue.Default ||
		j.MaxAttempts != queue.DefaultMaxAttempts || string(j.Payload) != `{"stack": "s1"}` {
		t.Errorf("claimed %+v", j)
	}
	enqueue(t, q, work, nil)
	if j := claimOne(t, q); !j.Account.IsZero() || string(j.Payload) != "null" {
		t.Errorf("installation job claimed with account %v, payload %s", j.Account, j.Payload)
	}
}
