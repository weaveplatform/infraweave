package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// failingSchedule is a scheduler store that fails the call fail names.
type failingSchedule struct {
	schedule.Store
	fail string
}

func (f failingSchedule) Queued(
	ctx context.Context,
	a kernel.AccountID,
	stack kernel.ULID,
) ([]lifecycle.Run, error) {
	if f.fail == "Queued" {
		return nil, errInjected
	}
	return f.Store.Queued(ctx, a, stack)
}

func (f failingSchedule) TakeLock(ctx context.Context, run lifecycle.Run) (bool, error) {
	if f.fail == "TakeLock" {
		return false, errInjected
	}
	return f.Store.TakeLock(ctx, run)
}

// scheduler is a scheduler of w's over store.
func (w *world) scheduler(store schedule.Store) *schedule.Scheduler {
	return schedule.New(schedule.Deps{
		DB: w.db, Store: store, Stacks: w.stacks, Lifecycle: w.lc, Queue: w.jobs,
	})
}

// claim claims w's pending jobs of kind k, dropping every other
// account's: the package's tests share one database.
func (w *world) claim(t *testing.T, k queue.Kind) []queue.Job {
	t.Helper()
	ctx := t.Context()
	if _, err := w.db.Exec(ctx, `DELETE FROM jobs WHERE kind = $1 AND account_id <> $2`,
		k.Name, w.acct); err != nil {
		t.Fatal(err)
	}
	jobs, err := w.jobs.Claim(ctx, k.Queue, []string{k.Name}, 100, time.Minute, "test")
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

// handle claims w's run.schedule jobs and runs each through s.
func (w *world) handle(t *testing.T, s *schedule.Scheduler) {
	t.Helper()
	jobs := w.claim(t, lifecycle.ScheduleKind)
	if len(jobs) != 1 {
		t.Fatalf("%d run.schedule jobs, want 1", len(jobs))
	}
	if err := s.Handle(t.Context(), jobs[0]); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := w.db.QueryRow(t.Context(), `SELECT state FROM jobs WHERE id = $1`,
		jobs[0].ID).Scan(&state); err != nil || state != "succeeded" {
		t.Errorf("the job is %q, %v after Handle; want succeeded", state, err)
	}
}

// state reads run's state.
func (w *world) state(t *testing.T, run lifecycle.Run) kernel.RunState {
	t.Helper()
	var s string
	if err := w.db.QueryRow(t.Context(), `SELECT state FROM runs WHERE account_id = $1 AND id = $2`,
		w.acct, run.ID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return kernel.RunState(s)
}

// TestAccScheduleLock checks that the walk moves the oldest blocking run
// to READY with the stack's run lock, keeps a later blocking run QUEUED
// with the first as its blocker, moves a proposed run past the lock, and
// once the first is canceled moves the second.
func TestAccScheduleLock(t *testing.T) {
	w := newWorld(t)
	s := w.scheduler(w.store)
	first, _, err := w.create(t, kernel.RunTypeTracked, "a")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := w.create(t, kernel.RunTypeTask, "b")
	if err != nil {
		t.Fatal(err)
	}
	proposed, _, err := w.create(t, kernel.RunTypeProposed, "c")
	if err != nil {
		t.Fatal(err)
	}
	w.handle(t, s)
	for _, c := range []struct {
		run  lifecycle.Run
		want kernel.RunState
	}{
		{first, kernel.RunStateReady},
		{second, kernel.RunStateQueued},
		{proposed, kernel.RunStateReady},
	} {
		if got := w.state(t, c.run); got != c.want {
			t.Errorf("%s run is %s, want %s", c.run.Type, got, c.want)
		}
	}
	blockers, err := core.New(w.store).Blockers(t.Context(), w.acct, []kernel.ULID{w.stack})
	if err != nil || blockers[w.stack].Run != first.ID {
		t.Errorf("Blockers = %v, %v; want the first run", blockers, err)
	}
	if p, _ := w.stacks.last(w.stack); p != (projection{"READY", true}) {
		t.Errorf("projection %+v, want READY and blocked", p)
	}

	first.State, first.Version = kernel.RunStateReady, 2
	w.mustMove(t, first, kernel.RunStateCanceled, machine.DriverAPI)
	w.handle(t, s)
	if got := w.state(t, second); got != kernel.RunStateReady {
		t.Errorf("the second run is %s after the first's cancel, want READY", got)
	}
	blockers, _ = core.New(w.store).Blockers(t.Context(), w.acct, []kernel.ULID{w.stack})
	if blockers[w.stack].Run != second.ID {
		t.Errorf("Blockers = %v, want the second run", blockers)
	}
}

// TestAccScheduleBurst checks that ten creations on one stack leave one
// run.schedule job, whose one walk moves the ten proposed runs.
func TestAccScheduleBurst(t *testing.T) {
	w := newWorld(t)
	for i := range 10 {
		if _, _, err := w.create(t, kernel.RunTypeProposed, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if n := w.schedules(t); n != 1 {
		t.Errorf("%d run.schedule jobs after ten creations, want 1", n)
	}
	w.handle(t, w.scheduler(w.store))
	var ready int
	if err := w.db.QueryRow(t.Context(), `SELECT count(*) FROM runs
		WHERE account_id = $1 AND state = 'READY'`, w.acct).Scan(&ready); err != nil || ready != 10 {
		t.Errorf("%d READY runs, %v; want 10", ready, err)
	}
}

// TestAccScheduleReplicas races walks of one stack from two replicas: each
// run reaches READY once, and one blocking run holds the lock.
func TestAccScheduleReplicas(t *testing.T) {
	w := newWorld(t)
	runs := make([]lifecycle.Run, 0, 10)
	for i := range 5 {
		for _, typ := range []kernel.RunType{kernel.RunTypeTracked, kernel.RunTypeTesting} {
			run, _, err := w.create(t, typ, fmt.Sprint(i))
			if err != nil {
				t.Fatal(err)
			}
			runs = append(runs, run)
		}
	}
	replicas := []*schedule.Scheduler{w.scheduler(w.store), w.scheduler(w.store)}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 5 {
		for _, s := range replicas {
			wg.Go(func() {
				errs <- w.in(t, func(ctx context.Context) error {
					return s.Schedule(ctx, w.acct, w.stack)
				})
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a walk failed: %v", err)
		}
	}
	ready := map[kernel.RunType]int{}
	for _, run := range runs {
		steps := w.steps(t, run.ID)
		n := 0
		for _, s := range steps {
			if s.State == "READY" {
				n++
			}
		}
		if n > 1 {
			t.Errorf("%s run %s reached READY %d times", run.Type, run.ID, n)
		}
		ready[run.Type] += n
	}
	if ready[kernel.RunTypeTracked] != 1 || ready[kernel.RunTypeTesting] != 5 {
		t.Errorf("READY by type %v, want 1 tracked and 5 testing", ready)
	}
}

// TestAccScheduleDisabled checks that a disabled stack's queued runs wait,
// that enabling it enqueues its scheduling, which moves them, and that
// other changes and a deleted stack schedule nothing.
func TestAccScheduleDisabled(t *testing.T) {
	w := newWorld(t)
	s := w.scheduler(w.store)
	run, _, err := w.create(t, kernel.RunTypeTracked, "a")
	if err != nil {
		t.Fatal(err)
	}
	st := w.stacks.settings[w.stack]
	st.Disabled = true
	w.stacks.settings[w.stack] = st
	w.handle(t, s)
	if got := w.state(t, run); got != kernel.RunStateQueued {
		t.Errorf("a disabled stack's run is %s, want QUEUED", got)
	}
	st.Disabled = false
	w.stacks.settings[w.stack] = st

	consume := queue.Kind{Name: "test.consume", Queue: queue.Events, Policy: queue.Ignore}
	changed := func(change string) error {
		t.Helper()
		if _, err := w.jobs.Enqueue(t.Context(), consume, struct{}{},
			queue.Account(w.acct.ULID)); err != nil {
			t.Fatal(err)
		}
		jobs := w.claim(t, consume)
		doc, _ := json.Marshal(stacksapi.Changed{Stack: w.stack, Slug: "network", Change: change})
		return s.StackChanged(t.Context(), outbox.Event{
			Account: w.acct.ULID, Kind: stacksapi.ChangedKind, Document: doc,
		}, jobs[0])
	}
	if err := changed(stacksapi.ChangeUpdated); err != nil || w.schedules(t) != 0 {
		t.Errorf("an update = %v with %d run.schedule jobs, want none", err, w.schedules(t))
	}
	if err := changed(stacksapi.ChangeEnabled); err != nil || w.schedules(t) != 1 {
		t.Errorf("enabling = %v with %d run.schedule jobs, want 1", err, w.schedules(t))
	}
	w.handle(t, s)
	if got := w.state(t, run); got != kernel.RunStateReady {
		t.Errorf("the run is %s once the stack is enabled, want READY", got)
	}
	if err := changed(stacksapi.ChangeUnlocked); err != nil || w.schedules(t) != 1 {
		t.Errorf("unlocking = %v with %d run.schedule jobs, want 1", err, w.schedules(t))
	}

	if err := w.in(t, func(ctx context.Context) error {
		return s.Schedule(ctx, w.acct, kernel.NewULID())
	}); err != nil {
		t.Errorf("scheduling a deleted stack = %v", err)
	}
	var syntax *json.SyntaxError
	if err := s.StackChanged(t.Context(), outbox.Event{Document: []byte("{")},
		queue.Job{}); !errors.As(err, &syntax) {
		t.Errorf("an undecodable event = %v", err)
	}
	if err := s.Handle(t.Context(), queue.Job{Payload: []byte("{")}); !errors.As(err, &syntax) {
		t.Errorf("an undecodable job = %v", err)
	}
}

// TestAccScheduleFailures fails each call a walk makes, and checks that
// the failure reaches the caller and leaves the run QUEUED.
func TestAccScheduleFailures(t *testing.T) {
	for _, fail := range []string{"LockForRun", "Queued", "TakeLock", "Lock", "Projection"} {
		t.Run(fail, func(t *testing.T) {
			w := newWorld(t)
			run, _, err := w.create(t, kernel.RunTypeTask, "a")
			if err != nil {
				t.Fatal(err)
			}
			if fail == "LockForRun" {
				w.stacks.err[fail] = errInjected
			}
			f := failing{Store: w.store, events: w.events, jobs: w.jobs, fail: fail}
			w.lc = w.lifecycle(f, f, f)
			s := w.scheduler(failingSchedule{Store: w.store, fail: fail})
			if err := w.in(t, func(ctx context.Context) error {
				return s.Schedule(ctx, w.acct, w.stack)
			}); !errors.Is(err, errInjected) {
				t.Errorf("Schedule = %v, want the injected failure", err)
			}
			if got := w.state(t, run); got != kernel.RunStateQueued {
				t.Errorf("the run is %s, want QUEUED", got)
			}
		})
	}
}

// TestAccScheduleStoreUnavailable fails the scheduler's store methods on a
// closed pool.
func TestAccScheduleStoreUnavailable(t *testing.T) {
	db := pool(t)
	closed := pg.New(db, nil)
	db.Close()
	ctx := context.Background()
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	if _, err := closed.Queued(ctx, acct, id); err == nil {
		t.Error("Queued on a closed pool succeeded")
	}
	if _, err := closed.TakeLock(ctx, lifecycle.Run{Account: acct, ID: id, Stack: id}); err == nil {
		t.Error("TakeLock on a closed pool succeeded")
	}
}
