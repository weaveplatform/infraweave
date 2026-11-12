package worker_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// overdueCall is one call of liveness.Overdue.
type overdueCall struct {
	now      time.Time
	timeouts []workersapi.PoolTimeout
	fallback time.Duration
	batch    int
}

// liveness is the runs domain's side of the reap: held is each worker's
// runs, failed the note each run failed with, overdue the Overdue calls,
// each answered with overdueRuns, and fail the method that fails with
// errDown. Each call outside a transaction fails.
type liveness struct {
	mu          sync.Mutex
	held        map[kernel.ULID][]kernel.ULID
	failed      map[kernel.ULID]string
	overdue     []overdueCall
	overdueRuns int
	fail        string
}

var errNoTx = errors.New("no transaction")

func newLiveness() *liveness {
	return &liveness{held: map[kernel.ULID][]kernel.ULID{}, failed: map[kernel.ULID]string{}}
}

func (l *liveness) check(ctx context.Context, method string) error {
	if !tx.In(ctx) {
		return errNoTx
	}
	if l.fail == method {
		return errDown
	}
	return nil
}

func (l *liveness) Lost(ctx context.Context, w kernel.ULID) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.check(ctx, "Lost"); err != nil {
		return 0, err
	}
	runs := l.held[w]
	for _, r := range runs {
		l.failed[r] = "worker lost"
	}
	delete(l.held, w)
	return len(runs), nil
}

func (l *liveness) Ended(
	ctx context.Context,
	w, run kernel.ULID,
	exit workersapi.Exit,
) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.check(ctx, "Ended"); err != nil {
		return false, err
	}
	i := slices.Index(l.held[w], run)
	if i < 0 {
		return false, nil
	}
	l.held[w] = slices.Delete(l.held[w], i, i+1)
	l.failed[run] = fmt.Sprintf("%s %d", exit.Reason, exit.Code)
	return true, nil
}

func (l *liveness) Overdue(
	ctx context.Context,
	now time.Time,
	timeouts []workersapi.PoolTimeout,
	fallback time.Duration,
	batch int,
) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.check(ctx, "Overdue"); err != nil {
		return 0, err
	}
	l.overdue = append(l.overdue, overdueCall{now, timeouts, fallback, batch})
	return l.overdueRuns, nil
}

// reap reaps f once, failing the test on an error.
func (f *fixture) reap(t *testing.T) worker.Reaped {
	t.Helper()
	r, err := f.svc.Reap(t.Context())
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	return r
}

// TestUnitWorkerReap checks, in virtual time, that a busy worker that
// stops polling is OFFLINE once worker.offline_after passes and keeps its
// run; that a poll at 30 minutes clears the mark and keeps the run; that
// once run.lost_after passes from its last poll the worker is deleted and
// its run fails with "worker lost"; and that its late poll is refused.
func TestUnitWorkerReap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		l := newLiveness()
		f.svc.BindLiveness(l)
		w, b := f.add()
		run := kernel.NewULID()
		l.held[w.ID] = []kernel.ULID{run}
		start := time.Now()
		busy := worker.PollInput{Busy: true, Wait: worker.MinWait}
		at := func(d time.Duration) { <-time.After(time.Until(start.Add(d))) }

		<-f.poll(b, busy)
		at(worker.DefaultOfflineAfter - time.Second)
		if r := f.reap(t); r != (worker.Reaped{}) ||
			f.store.workers[w.ID].Status() != worker.StatusBusy {
			t.Errorf("a second before offline_after: %+v, %s", r, f.store.workers[w.ID].Status())
		}
		at(worker.DefaultOfflineAfter + time.Second)
		if r := f.reap(t); r != (worker.Reaped{Offline: 1}) ||
			f.store.workers[w.ID].Status() != worker.StatusOffline || len(l.failed) != 0 {
			t.Errorf("past offline_after: %+v, %s, failed %v", r,
				f.store.workers[w.ID].Status(), l.failed)
		}

		at(30 * time.Minute)
		if h := <-f.poll(b, busy); h.err != nil {
			t.Fatalf("the poll at 30 minutes: %v", h.err)
		}
		if r := f.reap(t); r != (worker.Reaped{}) ||
			f.store.workers[w.ID].Status() != worker.StatusBusy {
			t.Errorf("after the poll at 30 minutes: %+v, %s", r, f.store.workers[w.ID].Status())
		}

		at(30*time.Minute + worker.DefaultLostAfter - time.Second)
		if r := f.reap(t); r != (worker.Reaped{Offline: 1}) || len(l.failed) != 0 {
			t.Errorf("a second before lost_after: %+v, failed %v", r, l.failed)
		}
		at(30*time.Minute + worker.DefaultLostAfter + time.Second)
		if r := f.reap(t); r != (worker.Reaped{Lost: 1, LostRuns: 1}) ||
			l.failed[run] != "worker lost" {
			t.Errorf("past lost_after: %+v, failed %v", r, l.failed)
		}
		if _, ok := f.store.workers[w.ID]; ok {
			t.Error("the lost worker is still registered")
		}
		if h := <-f.poll(b, busy); !errors.Is(h.err, worker.ErrUnauthenticated) {
			t.Errorf("the late poll = %v", h.err)
		}
	})
}

// TestUnitWorkerReapClocks checks that the reap takes its settings, and
// fails overdue preparations with each pool's own preparing timeout, the
// default one for the rest, at the reap's time.
func TestUnitWorkerReapClocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{
			OfflineAfter: 10 * time.Minute, LostAfter: 20 * time.Minute,
			PreparingTimeout: 7 * time.Minute,
		})
		l := newLiveness()
		l.overdueRuns = 3
		f.svc.BindLiveness(l)
		own := pool.Pool{ID: kernel.NewULID(), PreparingTimeout: 5 * time.Minute}
		f.store.pools[own.ID] = own
		w, _ := f.add()
		seen := f.store.workers[w.ID]
		seen.LastSeenAt = time.Now()
		f.store.workers[w.ID] = seen

		<-time.After(9 * time.Minute)
		if r := f.reap(t); r != (worker.Reaped{Overdue: 3}) {
			t.Errorf("inside offline_after: %+v", r)
		}
		<-time.After(19 * time.Minute)
		if r := f.reap(t); r != (worker.Reaped{Offline: 1, Lost: 1, Overdue: 3}) {
			t.Errorf("past lost_after: %+v", r)
		}
		want := []workersapi.PoolTimeout{{Pool: own.ID, Timeout: 5 * time.Minute}}
		last := l.overdue[len(l.overdue)-1]
		if len(l.overdue) != 2 || !last.now.Equal(time.Now()) ||
			!slices.Equal(last.timeouts, want) || last.fallback != 7*time.Minute ||
			last.batch != worker.ReapBatch {
			t.Errorf("Overdue calls %+v", l.overdue)
		}

		f = newFixture(t, worker.Deps{})
		l = newLiveness()
		f.svc.BindLiveness(l)
		f.reap(t)
		if len(l.overdue) != 1 || l.overdue[0].fallback != pool.DefaultPreparingTimeout {
			t.Errorf("the default preparing timeout: %+v", l.overdue)
		}
	})
}

// TestUnitWorkerReapBatch checks that one reap deletes at most ReapBatch
// lost workers, and the next goes on.
func TestUnitWorkerReapBatch(t *testing.T) {
	f := newFixture(t, worker.Deps{})
	f.svc.BindLiveness(newLiveness())
	for range worker.ReapBatch + 1 {
		f.add()
	}
	if r := f.reap(t); r.Lost != worker.ReapBatch || r.Offline != worker.ReapBatch+1 {
		t.Errorf("the first reap = %+v", r)
	}
	if r := f.reap(t); r.Lost != 1 || len(f.store.workers) != 0 {
		t.Errorf("the second reap = %+v, leaving %d", r, len(f.store.workers))
	}
}

// TestUnitWorkerReapUnbound checks that a reap with no liveness bound
// deletes lost workers and fails no run, reading no pool's timeout.
func TestUnitWorkerReapUnbound(t *testing.T) {
	f := newFixture(t, worker.Deps{})
	f.add()
	f.store.fail = "PoolTimeouts"
	if r := f.reap(t); r != (worker.Reaped{Offline: 1, Lost: 1}) {
		t.Errorf("Reap = %+v", r)
	}
}

// TestUnitWorkerReapFailures checks that a failing store or liveness
// fails the reap.
func TestUnitWorkerReapFailures(t *testing.T) {
	for _, fail := range []string{"MarkOffline", "Lost", "PoolTimeouts"} {
		f := newFixture(t, worker.Deps{})
		f.svc.BindLiveness(newLiveness())
		f.add()
		f.store.fail = fail
		if _, err := f.svc.Reap(t.Context()); !errors.Is(err, errDown) {
			t.Errorf("the store failing %s: %v", fail, err)
		}
	}
	for _, fail := range []string{"Lost", "Overdue"} {
		f := newFixture(t, worker.Deps{})
		l := newLiveness()
		l.fail = fail
		f.svc.BindLiveness(l)
		f.add()
		if _, err := f.svc.Reap(t.Context()); !errors.Is(err, errDown) {
			t.Errorf("the liveness failing %s: %v", fail, err)
		}
	}
}

// TestUnitWorkerPollEnded checks that a poll that is not busy and names
// a run fails it, with the exit it reports, when the worker holds it, and
// one with no exit as ended; that a busy poll, or one naming no run,
// fails nothing; that a failing liveness fails the poll; and that with no
// liveness bound the poll goes on.
func TestUnitWorkerPollEnded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		l := newLiveness()
		f.svc.BindLiveness(l)
		w, b := f.add()
		killed, gone, running := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
		l.held[w.ID] = []kernel.ULID{killed, gone, running}
		exit := &workersapi.Exit{Code: 137, Reason: workersapi.ExitKilled}
		for _, in := range []worker.PollInput{
			{Busy: true, Run: &running, Exit: exit},
			{},
			{Run: &killed, Exit: exit},
			{Run: &gone},
			{Run: &killed, Exit: exit},
		} {
			in.Wait = worker.MinWait
			if h := <-f.poll(b, in); h.err != nil {
				t.Fatalf("poll %+v: %v", in, h.err)
			}
		}
		want := map[kernel.ULID]string{killed: "killed 137", gone: " 0"}
		if fmt.Sprint(l.failed) != fmt.Sprint(want) || !slices.Equal(l.held[w.ID],
			[]kernel.ULID{running}) {
			t.Errorf("failed %v, holding %v", l.failed, l.held[w.ID])
		}

		l.fail = "Ended"
		if h := <-f.poll(b, worker.PollInput{Run: &running}); !errors.Is(h.err, errDown) {
			t.Errorf("a failing liveness: %v", h.err)
		}
		f = newFixture(t, worker.Deps{})
		_, b = f.add()
		h := <-f.poll(b, worker.PollInput{Run: &running, Exit: exit, Wait: worker.MinWait})
		if h.err != nil {
			t.Errorf("a poll with no liveness bound: %v", h.err)
		}
	})
}

// TestUnitWorkerStatusOffline checks that OFFLINE comes before every
// other status, and that a search filters by it.
func TestUnitWorkerStatusOffline(t *testing.T) {
	w := worker.Worker{Offline: true, Drained: true, Busy: true}
	if w.Status() != worker.StatusOffline {
		t.Errorf("Status() = %s", w.Status())
	}
	i := slices.IndexFunc(worker.Fields, func(f search.Field) bool { return f.Name == "status" })
	if i < 0 || !slices.Contains(worker.Fields[i].Values, string(worker.StatusOffline)) {
		t.Errorf("the status field does not filter by OFFLINE: %+v", worker.Fields)
	}
}
