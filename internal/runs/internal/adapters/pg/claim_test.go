package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// waits inserts acct's run of w's stack waiting in pool in state, of
// typ at priority p, a drift run when drift, created at.
func (w *world) waits(
	t *testing.T,
	acct kernel.AccountID,
	pool kernel.ULID,
	state kernel.RunState,
	typ kernel.RunType,
	p kernel.RunPriority,
	drift bool,
	at time.Time,
) kernel.ULID {
	t.Helper()
	id := kernel.NewULID()
	if _, err := w.db.Exec(t.Context(), `INSERT INTO runs (account_id, id, stack_id, type,
		state, commit, triggered_by, priority, drift_detection, priority_tier, worker_pool_id,
		created_at) VALUES ($1, $2, $3, $4, $5, '{"hash":"abc"}', 'alice', $6, $7, $8, $9, $10)`,
		acct, id, w.stack, string(typ), string(state), string(p), drift,
		kernel.RunTier(p, typ, drift), pool, at); err != nil {
		t.Fatal(err)
	}
	return id
}

// take claims through c for worker of w's account's pool, with the drift
// limit limit, in a transaction of its own.
func (w *world) take(
	t *testing.T,
	c *schedule.Claims,
	pool, worker kernel.ULID,
	limit *int,
) (workersapi.Assignment, bool, error) {
	t.Helper()
	var (
		a  workersapi.Assignment
		ok bool
	)
	err := w.in(t, func(ctx context.Context) error {
		var err error
		a, ok, err = c.Claim(ctx, workersapi.Claim{
			Account: w.acct, Pool: pool, Worker: worker, DriftLimit: limit,
		})
		return err
	})
	return a, ok, err
}

// held reads run's state and worker.
func (w *world) held(t *testing.T, run kernel.ULID) (kernel.RunState, *kernel.ULID) {
	t.Helper()
	var (
		state  string
		worker *kernel.ULID
	)
	if err := w.db.QueryRow(t.Context(), `SELECT state, worker_id FROM runs WHERE id = $1`,
		run).Scan(&state, &worker); err != nil {
		t.Fatal(err)
	}
	return kernel.RunState(state), worker
}

// TestAccClaim checks that claims take a pool's waiting runs by tier,
// then creation, each to the preparing state its claim edge names with
// the claimant as its worker; that a pool's drift runs below the first
// tier wait while its drift runs on workers reach a limit, and that a
// negative limit is none; that a worker holding a run claims none; that
// queued runs, other pools' runs and other accounts' runs are never
// claimed; and that a claim outside a transaction is refused.
func TestAccClaim(t *testing.T) {
	w := newWorld(t)
	c := schedule.NewClaims(w.store, w.lc)
	pool := kernel.NewULID()
	if _, _, err := c.Claim(t.Context(), workersapi.Claim{Account: w.acct, Pool: pool}); !errors.Is(
		err, lifecycle.ErrNoTransaction) {
		t.Errorf("a claim outside a transaction = %v", err)
	}
	base := w.now.Add(-time.Hour)
	ready, normal := kernel.RunStateReady, kernel.RunPriorityNormal
	proposed, tracked := kernel.RunTypeProposed, kernel.RunTypeTracked
	low := w.waits(t, w.acct, pool, ready, proposed, kernel.RunPriorityLow, false, base)
	first := w.waits(t, w.acct, pool, ready, proposed, kernel.RunPriorityHigh, false,
		base.Add(2*time.Second))
	confirmed := w.waits(t, w.acct, pool, kernel.RunStateConfirmed, tracked, normal, false, base)
	replan := w.waits(t, w.acct, pool, kernel.RunStateReplanRequested, tracked, normal, false,
		base.Add(time.Second))
	drift := w.waits(t, w.acct, pool, ready, proposed, normal, true, base)
	untouched := []kernel.ULID{
		w.waits(t, w.acct, pool, kernel.RunStateQueued, proposed, kernel.RunPriorityHigh, false,
			base),
		w.waits(t, w.acct, kernel.NewULID(), ready, proposed, kernel.RunPriorityHigh, false, base),
		w.waits(
			t,
			kernel.NewAccountID(),
			pool,
			ready,
			proposed,
			kernel.RunPriorityHigh,
			false,
			base,
		),
	}

	none := 0
	for i, want := range []workersapi.Assignment{
		{Run: first, State: kernel.RunStatePreparing},
		{Run: confirmed, State: kernel.RunStatePreparingApply},
		{Run: replan, State: kernel.RunStatePreparingReplan},
		{Run: low, State: kernel.RunStatePreparing},
	} {
		worker := kernel.NewULID()
		got, ok, err := w.take(t, c, pool, worker, &none)
		if err != nil || !ok || got != want {
			t.Errorf("claim %d = %+v, %v, %v, want %+v", i, got, ok, err, want)
		}
		if state, by := w.held(t, want.Run); state != want.State || by == nil || *by != worker {
			t.Errorf("claim %d left run %s %s by %v", i, want.Run, state, by)
		}
	}
	if got, ok, err := w.take(t, c, pool, kernel.NewULID(), &none); err != nil || ok {
		t.Errorf("a drift run past a limit of none = %+v, %v, %v", got, ok, err)
	}
	unlimited, holder := -1, kernel.NewULID()
	if got, ok, err := w.take(t, c, pool, holder, &unlimited); err != nil || !ok ||
		got.Run != drift {
		t.Errorf("a drift run under a negative limit = %+v, %v, %v", got, ok, err)
	}
	later := w.waits(t, w.acct, pool, ready, proposed, normal, false, base)
	if got, ok, err := w.take(t, c, pool, holder, nil); err != nil || ok {
		t.Errorf("a claim by a worker holding a run = %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := w.take(t, c, pool, kernel.NewULID(), nil); err != nil || !ok ||
		got.Run != later {
		t.Errorf("a claim with no limit = %+v, %v, %v", got, ok, err)
	}
	for _, id := range untouched {
		if state, by := w.held(t, id); by != nil ||
			(state != kernel.RunStateReady && state != kernel.RunStateQueued) {
			t.Errorf("run %s, which no claim may take, is %s by %v", id, state, by)
		}
	}
}

// TestAccClaimDriftRace checks that a claim of a pool's drift run waits
// for another claim of one that has not committed, then counts it: two
// claims at once under a limit of one take one drift run between them.
func TestAccClaimDriftRace(t *testing.T) {
	w := newWorld(t)
	c := schedule.NewClaims(w.store, w.lc)
	pool, one := kernel.NewULID(), 1
	ready, proposed := kernel.RunStateReady, kernel.RunTypeProposed
	base := w.now.Add(-time.Hour)
	firstRun := w.waits(t, w.acct, pool, ready, proposed, kernel.RunPriorityNormal, true, base)
	secondRun := w.waits(t, w.acct, pool, ready, proposed, kernel.RunPriorityLow, true, base)

	took, release, done := make(chan workersapi.Assignment, 1), make(chan struct{}),
		make(chan error, 1)
	go func() {
		done <- w.in(t, func(ctx context.Context) error {
			a, ok, err := c.Claim(ctx, workersapi.Claim{
				Account: w.acct, Pool: pool, Worker: kernel.NewULID(), DriftLimit: &one,
			})
			if err != nil || !ok {
				return errors.Join(err, errors.New("the first claim took nothing"))
			}
			took <- a
			<-release
			return nil
		})
	}()
	if a := <-took; a.Run != firstRun {
		t.Errorf("the first claim took %s, want %s", a.Run, firstRun)
	}
	type claim struct {
		a   workersapi.Assignment
		ok  bool
		err error
	}
	second := make(chan claim, 1)
	go func() {
		a, ok, err := w.take(t, c, pool, kernel.NewULID(), &one)
		second <- claim{a, ok, err}
	}()
	waitForLock(t, w.db)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := <-second; got.err != nil || got.ok {
		t.Errorf("the second claim = %+v, want none", got)
	}
	if state, by := w.held(t, secondRun); state != ready || by != nil {
		t.Errorf("the second drift run is %s by %v", state, by)
	}
}

// waitForLock returns once a session of db's database waits on a lock.
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

// failingClaims is a claim store that fails the call fail names: Next is
// the first read of a claim, and NextEarlier the read past the drift
// tiers.
type failingClaims struct {
	schedule.ClaimStore
	fail string
}

func (f failingClaims) Holding(
	ctx context.Context,
	a kernel.AccountID,
	w kernel.ULID,
) (bool, error) {
	if f.fail == "Holding" {
		return false, errInjected
	}
	return f.ClaimStore.Holding(ctx, a, w)
}

func (f failingClaims) Drifting(
	ctx context.Context,
	a kernel.AccountID,
	p kernel.ULID,
) (int, error) {
	if f.fail == "Drifting" {
		return 0, errInjected
	}
	return f.ClaimStore.Drifting(ctx, a, p)
}

func (f failingClaims) TakeDrift(ctx context.Context, a kernel.AccountID, p kernel.ULID) error {
	if f.fail == "TakeDrift" {
		return errInjected
	}
	return f.ClaimStore.TakeDrift(ctx, a, p)
}

func (f failingClaims) Next(
	ctx context.Context,
	a kernel.AccountID,
	p kernel.ULID,
	states []kernel.RunState,
	last int,
) (lifecycle.Run, bool, error) {
	if f.fail == "Next" || (f.fail == "NextEarlier" && last < 6) {
		return lifecycle.Run{}, false, errInjected
	}
	return f.ClaimStore.Next(ctx, a, p, states, last)
}

func (f failingClaims) Assign(ctx context.Context, run lifecycle.Run, worker kernel.ULID) error {
	if f.fail == "Assign" {
		return errInjected
	}
	return f.ClaimStore.Assign(ctx, run, worker)
}

// TestAccClaimFailures fails each call a claim makes, and checks that the
// failure reaches the caller and its transaction leaves the run waiting.
func TestAccClaimFailures(t *testing.T) {
	w := newWorld(t)
	ready, proposed, normal := kernel.RunStateReady, kernel.RunTypeProposed,
		kernel.RunPriorityNormal
	drifts, plain, none := kernel.NewULID(), kernel.NewULID(), 0
	drift := w.waits(t, w.acct, drifts, ready, proposed, normal, true, w.now)
	run := w.waits(t, w.acct, plain, ready, proposed, normal, false, w.now)
	for _, fail := range []string{"Holding", "Next", "TakeDrift", "Drifting", "NextEarlier"} {
		c := schedule.NewClaims(failingClaims{ClaimStore: w.store, fail: fail}, w.lc)
		if _, _, err := w.take(t, c, drifts, kernel.NewULID(), &none); !errors.Is(
			err, errInjected) {
			t.Errorf("%s failing = %v", fail, err)
		}
	}
	moving := w.lifecycle(failing{Store: w.store, events: w.events, jobs: w.jobs, fail: "Move"},
		w.events, w.jobs)
	for name, c := range map[string]*schedule.Claims{
		"Assign": schedule.NewClaims(failingClaims{ClaimStore: w.store, fail: "Assign"}, w.lc),
		"Move":   schedule.NewClaims(w.store, moving),
	} {
		if _, _, err := w.take(t, c, plain, kernel.NewULID(), nil); !errors.Is(err, errInjected) {
			t.Errorf("%s failing = %v", name, err)
		}
	}
	for _, id := range []kernel.ULID{drift, run} {
		if state, by := w.held(t, id); state != ready || by != nil {
			t.Errorf("run %s is %s by %v after a failed claim", id, state, by)
		}
	}
}

// TestAccClaimAnnounce checks that a run entering READY in a private
// pool notifies infraweave_runs of the pool and the run, and one in the
// public pool notifies nothing.
func TestAccClaimAnnounce(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	conn, err := w.db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+notify.Runs); err != nil {
		t.Fatal(err)
	}
	pool := kernel.NewULID()
	st := w.stacks.settings[w.stack]
	st.WorkerPool = &pool
	w.stacks.settings[w.stack] = st
	private := mustCreate(t, w, kernel.RunTypeProposed, "abc")
	st.WorkerPool = nil
	w.stacks.settings[w.stack] = st
	public := mustCreate(t, w, kernel.RunTypeProposed, "def")
	if private.WorkerPool == nil || *private.WorkerPool != pool || public.WorkerPool != nil {
		t.Errorf("pools %v and %v, want %s and none", private.WorkerPool, public.WorkerPool, pool)
	}
	if err := w.in(t, func(ctx context.Context) error {
		return w.scheduler(w.store).Schedule(ctx, w.acct, w.stack)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.Exec(ctx, `SELECT pg_notify($1, 'marker')`, notify.Runs); err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n.Payload == "marker" {
			break
		}
		got = append(got, n.Payload)
	}
	if want := workersapi.Waiting(pool, private.ID); len(got) != 1 || got[0] != want {
		t.Errorf("notified %q, want %q", got, want)
	}
}

// TestAccClaimStoreUnavailable checks that each claim call fails on a
// closed pool.
func TestAccClaimStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	db.Close()
	ctx := context.Background()
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	run := lifecycle.Run{Account: acct, ID: id, WorkerPool: &id}
	_, holdingErr := s.Holding(ctx, acct, id)
	_, driftingErr := s.Drifting(ctx, acct, id)
	_, _, nextErr := s.Next(ctx, acct, id, kernel.RunStates(), 7)
	for name, err := range map[string]error{
		"Holding": holdingErr, "Drifting": driftingErr, "Next": nextErr,
		"TakeDrift": s.TakeDrift(ctx, acct, id), "Assign": s.Assign(ctx, run, id),
		"Announce": s.Announce(ctx, run),
	} {
		if err == nil {
			t.Errorf("%s on a closed pool succeeded", name)
		}
	}
}
