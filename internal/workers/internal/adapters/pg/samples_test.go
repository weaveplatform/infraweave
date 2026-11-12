package pg_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// TestAccWorkerPoolSamples checks the samples store: a sample counts each
// pool's busy and registered workers, of every account, and its queued
// runs; a second sample of the same minute writes nothing, and two at
// once write one row a pool between them; Between answers an account's
// pool's window oldest first; and Prune deletes the samples before a
// time, a batch at a time.
func TestAccWorkerPoolSamples(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	p, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Builders"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	q, err := f.svc.Create(ctx, f.admin, pool.Input{Name: "Runners"}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	busy := f.newWorker(t, p, now)
	f.newWorker(t, p, now)
	if err := f.store.Claimed(ctx, busy.ID); err != nil {
		t.Fatal(err)
	}
	other, foreign := kernel.NewAccountID(), kernel.NewULID()
	if _, err := f.db.Exec(ctx, `INSERT INTO worker_pools (account_id, organization_id, id,
		name, space_id, certificate, serial) VALUES ($1, $2, $3, 'Builders', $1, '\x00', 1)`,
		other, f.org, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `INSERT INTO workers (account_id, organization_id, id, pool_id,
		serial, busy) VALUES ($1, $2, $3, $4, 1, true)`, other, f.org, kernel.NewULID(),
		foreign); err != nil {
		t.Fatal(err)
	}

	at := now.Truncate(time.Minute)
	window := func(acct kernel.AccountID, pool kernel.ULID, from, to time.Time) []workersapi.Sample {
		t.Helper()
		got, err := f.store.Between(ctx, acct, pool, from, to)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	sample := func(ctx context.Context, at time.Time, queued map[kernel.ULID]int) int {
		t.Helper()
		n, err := f.store.Sample(ctx, at, queued)
		if err != nil {
			t.Error(err)
		}
		return n
	}
	if n := sample(ctx, at, map[kernel.ULID]int{p.ID: 3, kernel.NewULID(): 9}); n != 3 {
		t.Errorf("the first sample wrote %d, want one a pool, 3", n)
	}
	if n := sample(ctx, at, map[kernel.ULID]int{p.ID: 5}); n != 0 {
		t.Errorf("a second sample of the minute wrote %d", n)
	}
	minute := at.Add(time.Minute)
	for _, c := range []struct {
		acct kernel.AccountID
		pool kernel.ULID
		want []workersapi.Sample
	}{
		{f.acct, p.ID, []workersapi.Sample{{At: at, Busy: 1, Registered: 2, Queued: 3}}},
		{f.acct, q.ID, []workersapi.Sample{{At: at}}},
		{other, foreign, []workersapi.Sample{{At: at, Busy: 1, Registered: 1}}},
		{f.acct, foreign, nil},
	} {
		if got := window(c.acct, c.pool, at, minute); !samplesEqual(got, c.want) {
			t.Errorf("%s's samples of %s = %+v, want %+v", c.acct, c.pool, got, c.want)
		}
	}

	wrote, release, done := make(chan int, 1), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
			wrote <- sample(ctx, minute, nil)
			<-release
			return nil
		})
	}()
	if n := <-wrote; n != 3 {
		t.Errorf("the first replica wrote %d, want 3", n)
	}
	second := make(chan int, 1)
	go func() { second <- sample(ctx, minute, nil) }()
	waitForLock(t, f.db)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := <-second; n != 0 {
		t.Errorf("the second replica, beside the first, wrote %d", n)
	}

	later := at.Add(3 * time.Minute)
	sample(ctx, later, nil)
	sample(ctx, at.Add(2*time.Minute), nil)
	got := window(f.acct, p.ID, at, later)
	if want := []time.Time{at, minute, at.Add(2 * time.Minute)}; !slices.EqualFunc(got, want,
		func(s workersapi.Sample, at time.Time) bool { return s.At.Equal(at) }) {
		t.Errorf("the window = %+v, want samples at %v", got, want)
	}
	if got := window(f.acct, p.ID, later, at); len(got) != 0 {
		t.Errorf("a window that ends before it starts = %+v", got)
	}

	for i, want := range []int{4, 2, 0} {
		if n, err := f.store.Prune(ctx, at.Add(2*time.Minute), 4); err != nil || n != want {
			t.Errorf("Prune %d = %d, %v, want %d", i, n, err, want)
		}
	}
	got = window(f.acct, p.ID, at, later.Add(time.Minute))
	if want := []time.Time{at.Add(2 * time.Minute), later}; !slices.EqualFunc(got, want,
		func(s workersapi.Sample, at time.Time) bool { return s.At.Equal(at) }) {
		t.Errorf("after the prune = %+v, want samples at %v", got, want)
	}
}

// samplesEqual reports whether a and b are the same samples, at the same
// instants.
func samplesEqual(a, b []workersapi.Sample) bool {
	return slices.EqualFunc(a, b, func(x, y workersapi.Sample) bool {
		return x.At.Equal(y.At) && x.Busy == y.Busy && x.Registered == y.Registered &&
			x.Queued == y.Queued
	})
}

// waitForLock waits until a session of db's database waits on a lock.
func waitForLock(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	for range 1000 {
		var n int
		if err := db.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		<-time.After(10 * time.Millisecond)
	}
	t.Fatal("no session waited on a lock")
}
