package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// working inserts a run of w's stack of typ in state, of pool, held by
// worker, its state changed at changed.
func (w *world) working(
	t *testing.T,
	typ kernel.RunType,
	state kernel.RunState,
	pool *kernel.ULID,
	worker kernel.ULID,
	changed time.Time,
) kernel.ULID {
	t.Helper()
	id := kernel.NewULID()
	if _, err := w.db.Exec(t.Context(), `INSERT INTO runs (account_id, id, stack_id, type,
		state, state_changed_at, commit, triggered_by, worker_pool_id, worker_id)
		VALUES ($1, $2, $3, $4, $5, $6, '{"hash":"abc"}', 'alice', $7, $8)`,
		w.acct, id, w.stack, string(typ), string(state), changed, pool, worker); err != nil {
		t.Fatal(err)
	}
	return id
}

// failedWith fails the test unless run is FAILED with note as its one
// failure.
func (w *world) failedWith(t *testing.T, run kernel.ULID, note string) {
	t.Helper()
	steps := w.steps(t, run)
	if state, _ := w.held(t, run); state != kernel.RunStateFailed || len(steps) != 1 ||
		steps[0].Note == nil || *steps[0].Note != note {
		t.Errorf("run %s is %s after %+v, want FAILED with %q", run, state, steps, note)
	}
}

// TestAccLivenessLost checks that a lost worker's runs on a worker fail
// with the note "worker lost", from each state on a worker of each type,
// and that its run in another state and another worker's run do not.
func TestAccLivenessLost(t *testing.T) {
	w := newWorld(t)
	l := schedule.NewLiveness(w.store, w.lc)
	lost, other := kernel.NewULID(), kernel.NewULID()
	if _, err := l.Lost(t.Context(), lost); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Errorf("Lost outside a transaction = %v", err)
	}
	at := w.now.Add(-time.Hour)
	held := []kernel.ULID{
		w.working(t, kernel.RunTypeProposed, kernel.RunStatePreparing, nil, lost, at),
		w.working(t, kernel.RunTypeProposed, kernel.RunStatePlanning, nil, lost, at),
		w.working(t, kernel.RunTypeTracked, kernel.RunStateApplying, nil, lost, at),
		w.working(t, kernel.RunTypeTask, kernel.RunStatePerforming, nil, lost, at),
		w.working(t, kernel.RunTypeTesting, kernel.RunStateApplying, nil, lost, at),
		w.working(t, kernel.RunTypeDestroy, kernel.RunStateDestroying, nil, lost, at),
	}
	waiting := w.working(t, kernel.RunTypeTracked, kernel.RunStateUnconfirmed, nil, lost, at)
	others := w.working(t, kernel.RunTypeProposed, kernel.RunStatePlanning, nil, other, at)
	for i, want := range []int{len(held), 0} {
		var n int
		if err := w.in(t, func(ctx context.Context) error {
			var err error
			n, err = l.Lost(ctx, lost)
			return err
		}); err != nil || n != want {
			t.Errorf("Lost %d = %d, %v, want %d", i, n, err, want)
		}
	}
	for _, run := range held {
		w.failedWith(t, run, schedule.NoteWorkerLost)
	}
	for run, want := range map[kernel.ULID]kernel.RunState{
		waiting: kernel.RunStateUnconfirmed, others: kernel.RunStatePlanning,
	} {
		if state, _ := w.held(t, run); state != want {
			t.Errorf("run %s is %s, want %s", run, state, want)
		}
	}
}

// TestAccLivenessEnded checks that a run whose container ended fails,
// with a note naming how, while its worker holds it, and that a run the
// worker no longer holds, or another worker's, does not.
func TestAccLivenessEnded(t *testing.T) {
	w := newWorld(t)
	l := schedule.NewLiveness(w.store, w.lc)
	worker := kernel.NewULID()
	ended := func(worker, run kernel.ULID, exit workersapi.Exit) (bool, error) {
		var failed bool
		err := w.in(t, func(ctx context.Context) error {
			var err error
			failed, err = l.Ended(ctx, worker, run, exit)
			return err
		})
		return failed, err
	}
	if _, err := l.Ended(t.Context(), worker, worker, workersapi.Exit{}); !errors.Is(
		err, lifecycle.ErrNoTransaction) {
		t.Errorf("Ended outside a transaction = %v", err)
	}
	for exit, note := range map[workersapi.Exit]string{
		{Code: 2, Reason: workersapi.ExitExited}:        "container exited with code 2",
		{Code: 0, Reason: workersapi.ExitExited}:        "container exited with code 0",
		{Code: 137, Reason: workersapi.ExitKilled}:      "container killed",
		{Code: 137, Reason: workersapi.ExitOutOfMemory}: "container out of memory",
		{Reason: workersapi.ExitMissing}:                "container missing",
		{}:                                              "container ended",
	} {
		run := w.working(t, kernel.RunTypeTracked, kernel.RunStateApplying, nil, worker, w.now)
		if failed, err := ended(kernel.NewULID(), run, exit); err != nil || failed {
			t.Errorf("another worker's end of %+v = %v, %v", exit, failed, err)
		}
		if failed, err := ended(worker, run, exit); err != nil || !failed {
			t.Errorf("Ended %+v = %v, %v", exit, failed, err)
		}
		if failed, err := ended(worker, run, exit); err != nil || failed {
			t.Errorf("Ended %+v again = %v, %v", exit, failed, err)
		}
		w.failedWith(t, run, note)
	}
}

// TestAccLivenessOverdue checks that preparations fail with the note
// "preparing timeout" once their pool's own timeout, or else the
// fallback, passes, oldest first and batch at a time; that runs inside
// their timeout and runs in other states do not; and that two reaps at
// once fail each run once, the second skipping the rows the first holds.
func TestAccLivenessOverdue(t *testing.T) {
	w := newWorld(t)
	l := schedule.NewLiveness(w.store, w.lc)
	if _, err := l.Overdue(t.Context(), w.now, nil, time.Minute, 1); !errors.Is(
		err, lifecycle.ErrNoTransaction) {
		t.Errorf("Overdue outside a transaction = %v", err)
	}
	custom, plain, worker := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	timeouts := []workersapi.PoolTimeout{{Pool: custom, Timeout: 5 * time.Minute}}
	fallback := 15 * time.Minute
	ago := func(d time.Duration) time.Time { return w.now.Add(-d) }
	proposed, tracked := kernel.RunTypeProposed, kernel.RunTypeTracked
	customLate := w.working(
		t,
		proposed,
		kernel.RunStatePreparing,
		&custom,
		worker,
		ago(6*time.Minute),
	)
	plainLate := w.working(t, tracked, kernel.RunStatePreparingReplan, &plain, worker,
		ago(16*time.Minute))
	publicLate := w.working(t, proposed, kernel.RunStatePreparing, nil, worker, ago(20*time.Minute))
	inTime := []kernel.ULID{
		w.working(t, tracked, kernel.RunStatePreparingApply, &custom, worker, ago(4*time.Minute)),
		w.working(t, proposed, kernel.RunStatePreparing, &plain, worker, ago(14*time.Minute)),
		w.working(t, proposed, kernel.RunStatePlanning, &plain, worker, ago(2*time.Hour)),
	}
	overdue := func(batch int) (int, error) {
		var n int
		err := w.in(t, func(ctx context.Context) error {
			var err error
			n, err = l.Overdue(ctx, w.now, timeouts, fallback, batch)
			return err
		})
		return n, err
	}
	if n, err := overdue(2); err != nil || n != 2 {
		t.Fatalf("the first batch = %d, %v, want 2", n, err)
	}
	if state, _ := w.held(t, customLate); state != kernel.RunStatePreparing {
		t.Errorf("the newest overdue run failed in the first batch of two: %s", state)
	}
	if n, err := overdue(10); err != nil || n != 1 {
		t.Errorf("the second batch = %d, %v, want 1", n, err)
	}
	if n, err := overdue(10); err != nil || n != 0 {
		t.Errorf("the third batch = %d, %v, want 0", n, err)
	}
	for _, run := range []kernel.ULID{customLate, plainLate, publicLate} {
		w.failedWith(t, run, schedule.NotePreparingTimeout)
	}
	for _, run := range inTime {
		if state, _ := w.held(t, run); state == kernel.RunStateFailed {
			t.Errorf("run %s, in its timeout or past preparing, failed", run)
		}
	}

	// The two runs are of different stacks: a run that fails schedules its
	// stack, and the second reap would wait on the first's schedule of the
	// same stack until the first commits.
	first := w.working(t, proposed, kernel.RunStatePreparing, nil, worker, ago(time.Hour))
	second := w.working(t, proposed, kernel.RunStatePreparing, nil, worker, ago(time.Hour-1))
	st := w.stacks.settings[w.stack]
	st.ID, st.Slug = kernel.NewULID(), "storage"
	w.stacks.settings[st.ID] = st
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET stack_id = $2 WHERE id = $1`, second,
		st.ID); err != nil {
		t.Fatal(err)
	}
	took, release, done := make(chan int, 1), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- w.in(t, func(ctx context.Context) error {
			n, err := l.Overdue(ctx, w.now, timeouts, fallback, 1)
			took <- n
			<-release
			return err
		})
	}()
	if n := <-took; n != 1 {
		t.Fatalf("the first reap failed %d runs, want 1", n)
	}
	if n, err := overdue(10); err != nil || n != 1 {
		t.Errorf("the second reap, beside the first, = %d, %v, want 1", n, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	w.failedWith(t, first, schedule.NotePreparingTimeout)
	w.failedWith(t, second, schedule.NotePreparingTimeout)
}

// failingLiveness is a liveness store that fails the call fail names.
type failingLiveness struct {
	schedule.LivenessStore
	fail string
}

func (f failingLiveness) Held(ctx context.Context, worker kernel.ULID) ([]lifecycle.Run, error) {
	if f.fail == "Held" {
		return nil, errInjected
	}
	return f.LivenessStore.Held(ctx, worker)
}

func (f failingLiveness) Overdue(
	ctx context.Context,
	now time.Time,
	timeouts []workersapi.PoolTimeout,
	fallback time.Duration,
	batch int,
) ([]lifecycle.Run, error) {
	if f.fail == "Overdue" {
		return nil, errInjected
	}
	return f.LivenessStore.Overdue(ctx, now, timeouts, fallback, batch)
}

// TestAccLivenessFailures fails the store's reads and the lifecycle's
// move under each liveness call, and checks that the failure reaches the
// caller and its transaction leaves the run on its worker.
func TestAccLivenessFailures(t *testing.T) {
	w := newWorld(t)
	worker := kernel.NewULID()
	run := w.working(t, kernel.RunTypeProposed, kernel.RunStatePreparing, nil, worker,
		w.now.Add(-time.Hour))
	moving := w.lifecycle(failing{Store: w.store, events: w.events, jobs: w.jobs, fail: "Move"},
		w.events, w.jobs)
	calls := map[string]func(l *schedule.Liveness) func(ctx context.Context) error{
		"Lost": func(l *schedule.Liveness) func(ctx context.Context) error {
			return func(ctx context.Context) error {
				_, err := l.Lost(ctx, worker)
				return err
			}
		},
		"Ended": func(l *schedule.Liveness) func(ctx context.Context) error {
			return func(ctx context.Context) error {
				_, err := l.Ended(ctx, worker, run, workersapi.Exit{})
				return err
			}
		},
		"Overdue": func(l *schedule.Liveness) func(ctx context.Context) error {
			return func(ctx context.Context) error {
				_, err := l.Overdue(ctx, w.now, nil, time.Minute, 10)
				return err
			}
		},
	}
	// Each failure is reached by the calls that make it.
	for _, c := range []struct {
		fail  string
		l     *schedule.Liveness
		calls []string
	}{
		{
			"Held", schedule.NewLiveness(failingLiveness{LivenessStore: w.store, fail: "Held"}, w.lc),
			[]string{"Lost", "Ended"},
		},
		{"Overdue", schedule.NewLiveness(
			failingLiveness{LivenessStore: w.store, fail: "Overdue"}, w.lc), []string{"Overdue"}},
		{"Move", schedule.NewLiveness(w.store, moving), []string{"Lost", "Ended", "Overdue"}},
	} {
		for _, call := range c.calls {
			if err := w.in(t, calls[call](c.l)); !errors.Is(err, errInjected) {
				t.Errorf("%s under %s failing = %v", call, c.fail, err)
			}
		}
	}
	if state, _ := w.held(t, run); state != kernel.RunStatePreparing {
		t.Errorf("the run is %s after failed calls", state)
	}
}

// TestAccLivenessStoreUnavailable checks that each liveness read fails on
// a closed pool.
func TestAccLivenessStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db, nil)
	db.Close()
	ctx := context.Background()
	if _, err := s.Held(ctx, kernel.NewULID()); err == nil {
		t.Error("Held on a closed pool succeeded")
	}
	if _, err := s.Overdue(ctx, time.Now(), nil, time.Minute, 1); err == nil {
		t.Error("Overdue on a closed pool succeeded")
	}
}
