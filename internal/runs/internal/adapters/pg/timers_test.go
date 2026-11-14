package pg_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/timers"
)

// aged inserts a run of w's stack of typ in state, created at created and
// its state changed at changed, held by no worker.
func (w *world) aged(
	t *testing.T,
	typ kernel.RunType,
	state kernel.RunState,
	created, changed time.Time,
) kernel.ULID {
	t.Helper()
	id := kernel.NewULID()
	if _, err := w.db.Exec(t.Context(), `INSERT INTO runs (account_id, id, stack_id, type,
		state, state_changed_at, created_at, commit, triggered_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '{"hash":"abc"}', 'alice')`,
		w.acct, id, w.stack, string(typ), string(state), changed, created); err != nil {
		t.Fatal(err)
	}
	return id
}

// timers is run.timers over w's store and lifecycle, on w's clock.
func (w *world) timers() *timers.Service {
	return timers.New(timers.Deps{
		DB: w.db, Store: w.store, Lifecycle: w.lc, Settings: timers.DefaultSettings(),
		Now: func() time.Time { return w.now },
	})
}

// TestAccRunTimers checks the query's boundary at PostgreSQL's precision:
// a run whose state changed exactly the rule's time ago ends and one a
// microsecond later does not; that the move records its note and the rule
// on its run_updated event and releases the stack run lock a blocking run
// holds; and that a run on a worker, however old, is left to liveness.
func TestAccRunTimers(t *testing.T) {
	w := newWorld(t)
	now := w.now.Truncate(time.Microsecond)
	week := 7 * 24 * time.Hour
	due := w.aged(t, kernel.RunTypeTracked, kernel.RunStateUnconfirmed, now.Add(-2*week),
		now.Add(-week))
	early := w.aged(t, kernel.RunTypeTracked, kernel.RunStateUnconfirmed, now.Add(-2*week),
		now.Add(-week+time.Microsecond))
	ready := w.aged(t, kernel.RunTypeTracked, kernel.RunStateReady, now.Add(-2*week),
		now.Add(-week))
	w.takeLock(t, lifecycle.Run{ID: ready, Stack: w.stack})
	worker := w.working(t, kernel.RunTypeTracked, kernel.RunStatePlanning, nil, kernel.NewULID(),
		now.Add(-40*24*time.Hour))
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET created_at = $2 WHERE id = $1`, worker,
		now.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	ended, err := w.timers().Pass(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(ended) != 2 || ended[runsapi.TimerUnconfirmed] != 1 ||
		ended[runsapi.TimerPickupBlocking] != 1 {
		t.Errorf("ended %v", ended)
	}
	for run, want := range map[kernel.ULID]kernel.RunState{
		due: kernel.RunStateDiscarded, early: kernel.RunStateUnconfirmed,
		ready: kernel.RunStateCanceled, worker: kernel.RunStatePlanning,
	} {
		if state, _ := w.held(t, run); state != want {
			t.Errorf("run %s is %s, want %s", run, state, want)
		}
	}
	if steps := w.steps(t, ready); len(steps) != 1 || steps[0].Note == nil ||
		*steps[0].Note != "not picked up within 7 days" || steps[0].Username != nil {
		t.Errorf("the canceled run's transitions %+v", steps)
	}
	if ev := w.updates(t, ready); len(ev) != 1 || ev[0].Doc.Timer != runsapi.TimerPickupBlocking ||
		ev[0].Doc.State != kernel.RunStateCanceled {
		t.Errorf("the canceled run's events %+v", ev)
	}
	var locks int
	if err := w.db.QueryRow(t.Context(), `SELECT count(*) FROM stack_run_locks
		WHERE account_id = $1 AND stack_id = $2`, w.acct, w.stack).Scan(&locks); err != nil ||
		locks != 0 {
		t.Errorf("%d stack run locks left, %v", locks, err)
	}
}

// TestAccRunTimersConcurrent checks that two passes at once, as two
// replicas' run.timers would be, end each due run once between them.
func TestAccRunTimersConcurrent(t *testing.T) {
	w := newWorld(t)
	old := w.now.Add(-3 * 24 * time.Hour)
	const runs = timers.Batch + timers.Batch/2
	for range runs {
		w.aged(t, kernel.RunTypeProposed, kernel.RunStateReady, old, old)
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)
	for range 2 {
		wg.Go(func() {
			ended, err := w.timers().Pass(context.WithoutCancel(t.Context()))
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			defer mu.Unlock()
			total += ended[runsapi.TimerPickupProposed]
		})
	}
	wg.Wait()
	var steps, left int
	if err := w.db.QueryRow(t.Context(), `SELECT
			(SELECT count(*) FROM run_state_transitions WHERE account_id = $1),
			(SELECT count(*) FROM runs WHERE account_id = $1 AND state <> 'CANCELED')`,
		w.acct).Scan(&steps, &left); err != nil {
		t.Fatal(err)
	}
	if total != runs || steps != runs || left != 0 {
		t.Errorf("ended %d, %d transitions, %d left, want %d once each", total, steps, left, runs)
	}
}
