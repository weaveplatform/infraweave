package worker_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// held is a poll's answer and how long it was held.
type held struct {
	got  worker.PollResult
	err  error
	took time.Duration
}

// add puts an idle worker of the fixture's pool in the store, and answers
// a bearer for it valid for an hour.
func (f *fixture) add() (worker.Worker, accessapi.Bearer) {
	w := worker.Worker{
		Account: f.pool.Account, Organization: f.pool.Organization, ID: kernel.NewULID(),
		Pool: f.pool.ID, Serial: f.pool.Serial,
	}
	f.store.workers[w.ID] = w
	return w, accessapi.Bearer{ID: w.ID, Expires: time.Now().Add(time.Hour)}
}

// poll starts a poll and answers its result once it returns.
func (f *fixture) poll(b accessapi.Bearer, in worker.PollInput) <-chan held {
	out := make(chan held, 1)
	start := time.Now()
	go func() {
		got, err := f.svc.Poll(context.Background(), b, in)
		out <- held{got: got, err: err, took: time.Since(start)}
	}()
	return out
}

// pending checks that the poll has not returned once every goroutine of
// the bubble is blocked.
func pending(t *testing.T, ch <-chan held) {
	t.Helper()
	synctest.Wait()
	select {
	case h := <-ch:
		t.Fatalf("returned early: %+v", h)
	default:
	}
}

// TestUnitWorkerPollHold checks that a poll holds for the wait it asks
// for, within the bounds, and returns with no signal when nothing changes.
func TestUnitWorkerPollHold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		max, wait time.Duration
		want      time.Duration
	}{
		{name: "default", want: worker.DefaultWait},
		{name: "asked", wait: 7 * time.Second, want: 7 * time.Second},
		{name: "below the minimum", wait: time.Second, want: worker.MinWait},
		{name: "above the maximum", wait: time.Hour, want: worker.MaxWait},
		{name: "server maximum", max: 30 * time.Second, wait: 90 * time.Second, want: 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, worker.Deps{PollMax: tc.max})
				w, b := f.add()
				h := <-f.poll(b, worker.PollInput{Busy: true, Wait: tc.wait})
				if h.err != nil || h.took != tc.want || h.got.Drained || h.got.Signals != nil ||
					h.got.Token != nil {
					t.Errorf("poll = %+v, want held %v", h, tc.want)
				}
				if seen := f.store.workers[w.ID]; !seen.Busy ||
					!seen.LastSeenAt.Equal(time.Now().Add(-h.took)) {
					t.Errorf("seen %+v", seen)
				}
			})
		})
	}
}

// TestUnitWorkerPollOnce checks that a worker's next poll ends its held
// one at once, so each worker holds one poll, and that another worker's
// poll ends none.
func TestUnitWorkerPollOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		_, b := f.add()
		_, other := f.add()
		first := f.poll(b, worker.PollInput{})
		pending(t, first)
		<-time.After(3 * time.Second)
		neighbour := f.poll(other, worker.PollInput{})
		pending(t, first)
		second := f.poll(b, worker.PollInput{})
		if h := <-first; h.err != nil || h.took != 3*time.Second || h.got.Signals != nil {
			t.Errorf("the superseded poll = %+v", h)
		}
		pending(t, second)
		for name, ch := range map[string]<-chan held{"second": second, "neighbour": neighbour} {
			if h := <-ch; h.err != nil || h.took != worker.DefaultWait {
				t.Errorf("the %s poll = %+v", name, h)
			}
		}
	})
}

// TestUnitWorkerPollSignals checks that a held poll returns early when
// its worker is drained or undrained, or its pool cycled, and goes on
// holding through a wake-up that changes nothing.
func TestUnitWorkerPollSignals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		w, b := f.add()
		other := f.pool
		other.ID = kernel.NewULID()
		f.store.pools[other.ID] = other
		ctx := t.Context()

		ch := f.poll(b, worker.PollInput{})
		pending(t, ch)
		f.svc.Wake(w.Pool)
		f.svc.Wake(other.ID)
		f.svc.WakeAll()
		pending(t, ch)
		<-time.After(10 * time.Second)
		if _, err := f.svc.DrainSet(
			ctx,
			w.Account,
			w.Pool.String(),
			w.ID.String(),
			true,
		); err != nil {
			t.Fatal(err)
		}
		if h := <-ch; h.err != nil || !h.got.Drained || h.took != 10*time.Second {
			t.Errorf("drained = %+v", h)
		}

		// A launcher that has not learned of the drain is answered at once.
		if h := <-f.poll(b, worker.PollInput{}); h.err != nil || !h.got.Drained || h.took != 0 {
			t.Errorf("drained before the poll = %+v", h)
		}
		ch = f.poll(b, worker.PollInput{Drained: true})
		pending(t, ch)
		if _, err := f.svc.DrainSet(
			ctx,
			w.Account,
			w.Pool.String(),
			w.ID.String(),
			false,
		); err != nil {
			t.Fatal(err)
		}
		if h := <-ch; h.err != nil || h.got.Drained || h.took != 0 {
			t.Errorf("undrained = %+v", h)
		}

		ch = f.poll(b, worker.PollInput{})
		pending(t, ch)
		if err := f.svc.Cycle(ctx, w.Account, w.Pool.String()); err != nil {
			t.Fatal(err)
		}
		if h := <-ch; h.err != nil || !slices.Equal(h.got.Signals, []string{worker.SignalCycle}) ||
			h.took != 0 {
			t.Errorf("cycled = %+v", h)
		}
		// A cycled worker is answered at once until it deregisters.
		if h := <-f.poll(b, worker.PollInput{}); h.took != 0 || len(h.got.Signals) != 1 {
			t.Errorf("after the cycle = %+v", h)
		}
		if err := f.svc.Deregister(ctx, b); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.Deregister(ctx, b); !errors.Is(err, worker.ErrUnauthenticated) {
			t.Errorf("deregister twice = %v", err)
		}
	})
}

// TestUnitWorkerPollRenew checks that a poll whose session has less than
// RenewWithin left answers at once with a renewed token.
func TestUnitWorkerPollRenew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		w, b := f.add()
		b.Expires = time.Now().Add(worker.RenewWithin - time.Second)
		h := <-f.poll(b, worker.PollInput{})
		if h.err != nil || h.took != 0 || h.got.Token == nil ||
			h.got.Token.Raw != "session "+w.ID.String() {
			t.Errorf("renewed = %+v", h)
		}
		f.svc = worker.New(worker.Deps{Store: f.store, DB: db{}, Tokens: tokens{fail: true}})
		if h := <-f.poll(b, worker.PollInput{}); !errors.Is(h.err, errDown) {
			t.Errorf("mint down = %+v", h)
		}
	})
}

// TestUnitWorkerPollStop checks that Stop answers a held poll at once,
// and every later one without a hold.
func TestUnitWorkerPollStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		_, b := f.add()
		ch := f.poll(b, worker.PollInput{})
		pending(t, ch)
		<-time.After(time.Second)
		f.svc.Stop()
		if h := <-ch; h.err != nil || h.took != time.Second {
			t.Errorf("stopped = %+v", h)
		}
		if h := <-f.poll(b, worker.PollInput{}); h.err != nil || h.took != 0 {
			t.Errorf("after stop = %+v", h)
		}
	})
}

// TestUnitWorkerPollRefused checks that a poll for a worker that is gone
// or registered under a replaced certificate is ErrUnauthenticated, at
// once or when it is woken, that a failing store fails it, and that a
// cancelled request ends its hold.
func TestUnitWorkerPollRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		w, b := f.add()
		if h := <-f.poll(accessapi.Bearer{ID: kernel.NewULID(), Expires: b.Expires},
			worker.PollInput{}); !errors.Is(h.err, worker.ErrUnauthenticated) {
			t.Errorf("unknown worker = %+v", h)
		}

		reset := func(serial int64) {
			p := f.pool
			p.Serial = serial
			f.store.pools[p.ID] = p
		}
		reset(3)
		if h := <-f.poll(b, worker.PollInput{}); !errors.Is(h.err, worker.ErrUnauthenticated) {
			t.Errorf("reset serial = %+v", h)
		}
		reset(2)

		ch := f.poll(b, worker.PollInput{})
		pending(t, ch)
		reset(3)
		f.svc.Wake(w.Pool)
		if h := <-ch; !errors.Is(h.err, worker.ErrUnauthenticated) {
			t.Errorf("reset while held = %+v", h)
		}
		reset(2)

		ch = f.poll(b, worker.PollInput{})
		pending(t, ch)
		delete(f.store.workers, w.ID)
		f.svc.Wake(w.Pool)
		if h := <-ch; !errors.Is(h.err, worker.ErrUnauthenticated) {
			t.Errorf("removed while held = %+v", h)
		}

		_, b = f.add()
		f.store.fail = "Seen"
		if h := <-f.poll(b, worker.PollInput{}); !errors.Is(h.err, errDown) {
			t.Errorf("Seen down = %+v", h)
		}
		f.store.fail = "Worker"
		if h := <-f.poll(b, worker.PollInput{}); !errors.Is(h.err, errDown) {
			t.Errorf("Worker down = %+v", h)
		}
		f.store.fail = ""

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := f.svc.Poll(ctx, b, worker.PollInput{})
			done <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled = %v", err)
		}
	})
}

// TestUnitWorkerManage checks the pool operations on workers: a drain or
// cycle of a pool or worker the account lacks, and the searches.
func TestUnitWorkerManage(t *testing.T) {
	f := newFixture(t, worker.Deps{})
	f.store.wake = func(kernel.ULID) {}
	w, _ := f.add()
	ctx := t.Context()
	acct := w.Account
	for name, err := range map[string]error{
		"pool not a ULID": func() error {
			_, err := f.svc.DrainSet(ctx, acct, "nope", w.ID.String(), true)
			return err
		}(),
		"another account's pool": func() error {
			_, err := f.svc.DrainSet(ctx, kernel.NewAccountID(), w.Pool.String(), w.ID.String(), true)
			return err
		}(),
		"cycle of an unknown pool": f.svc.Cycle(ctx, acct, kernel.NewULID().String()),
		"search of an unknown pool": func() error {
			_, err := f.svc.Search(ctx, acct, "nope", search.Input{})
			return err
		}(),
		"suggest of an unknown pool": func() error {
			_, err := f.svc.Suggest(ctx, acct, "nope", search.SuggestionsInput{})
			return err
		}(),
	} {
		if !errors.Is(err, pool.ErrNotFound) {
			t.Errorf("%s = %v", name, err)
		}
	}
	for _, id := range []string{"nope", kernel.NewULID().String()} {
		_, err := f.svc.DrainSet(ctx, acct, w.Pool.String(), id, true)
		if m, ok := errors.AsType[*pool.Missing](err); !ok || m.Kind != "worker" || m.ID != id {
			t.Errorf("drain of worker %q = %v", id, err)
		}
	}
	got, err := f.svc.Search(ctx, acct, w.Pool.String(), search.Input{})
	if err != nil || len(got.Edges) != 1 || got.Edges[0].Node.ID != w.ID {
		t.Errorf("search = %+v, %v", got, err)
	}
	f.store.fail = "SuggestWorkers"
	if _, err := f.svc.Suggest(
		ctx,
		acct,
		w.Pool.String(),
		search.SuggestionsInput{},
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("suggest = %v", err)
	}
	f.store.fail = "Cycle"
	if err := f.svc.Cycle(ctx, acct, w.Pool.String()); !errors.Is(err, errDown) {
		t.Errorf("cycle = %v", err)
	}
	of, err := f.svc.OfPools(ctx, acct, []kernel.ULID{w.Pool})
	if err != nil || len(of[w.Pool]) != 1 {
		t.Errorf("of pools = %+v, %v", of, err)
	}
	if w.Status() != worker.StatusIdle {
		t.Errorf("status = %s", w.Status())
	}
	w.Busy = true
	if w.Status() != worker.StatusBusy {
		t.Errorf("busy status = %s", w.Status())
	}
	w.Drained = true
	if w.Status() != worker.StatusDrained {
		t.Errorf("drained status = %s", w.Status())
	}
}

// scheduler hands out runs in order, recording each claim; err fails
// every claim. Its handover is the one Handover answers for any run, with
// handErr, recording each run it is asked for.
type scheduler struct {
	mu       sync.Mutex
	runs     []workersapi.Assignment
	claims   []workersapi.Claim
	err      error
	handover workersapi.Handover
	handErr  error
	handed   []kernel.ULID
}

func (s *scheduler) Handover(_ context.Context, run kernel.ULID) (workersapi.Handover, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handed = append(s.handed, run)
	return s.handover, s.handErr
}

func (s *scheduler) Claim(
	_ context.Context,
	c workersapi.Claim,
) (workersapi.Assignment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims = append(s.claims, c)
	if s.err != nil || len(s.runs) == 0 {
		return workersapi.Assignment{}, false, s.err
	}
	a := s.runs[0]
	s.runs = s.runs[1:]
	return a, true, nil
}

// wait makes a run wait for a claim.
func (s *scheduler) wait() workersapi.Assignment {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := workersapi.Assignment{Run: kernel.NewULID(), State: kernel.RunStatePreparing}
	s.runs = append(s.runs, a)
	return a
}

// claimed reports whether h is an answer with a, with its run token.
func claimed(h held, a workersapi.Assignment) bool {
	got := h.got.Assignment
	return h.err == nil && got != nil && got.Run == a.Run && got.State == a.State &&
		got.Token.Raw == "run "+a.Run.String()
}

// TestUnitWorkerPollClaim checks that an idle, undrained worker's poll
// claims its pool's next run before it holds, with the pool's drift
// limit and a run token, that the claim marks the worker busy, and that
// a busy, drained or cycled worker claims nothing.
func TestUnitWorkerPollClaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		sch := &scheduler{}
		f.svc.BindScheduler(sch)
		limit := int32(2)
		p := f.pool
		p.DriftDetectionRunLimit = &limit
		f.store.pools[p.ID] = p
		w, b := f.add()

		a := sch.wait()
		if h := <-f.poll(b, worker.PollInput{}); !claimed(h, a) || h.took != 0 {
			t.Errorf("claim = %+v", h)
		}
		if !f.store.workers[w.ID].Busy {
			t.Error("the claim left the worker idle")
		}
		want := workersapi.Claim{Account: w.Account, Pool: w.Pool, Worker: w.ID}
		if len(sch.claims) != 1 || sch.claims[0].DriftLimit == nil ||
			*sch.claims[0].DriftLimit != 2 {
			t.Fatalf("claims = %+v", sch.claims)
		}
		sch.claims[0].DriftLimit = nil
		if sch.claims[0] != want {
			t.Errorf("claim = %+v, want %+v", sch.claims[0], want)
		}

		sch.wait()
		sch.claims = nil
		if h := <-f.poll(b, worker.PollInput{Busy: true, Wait: worker.MinWait}); h.err != nil ||
			h.got.Assignment != nil {
			t.Errorf("busy = %+v", h)
		}
		for name, set := range map[string]func(*worker.Worker){
			"drained": func(w *worker.Worker) { w.Drained = true },
			"cycled":  func(w *worker.Worker) { w.Cycle = true },
		} {
			cur := w
			set(&cur)
			f.store.workers[w.ID] = cur
			if h := <-f.poll(b, worker.PollInput{Drained: cur.Drained}); h.err != nil ||
				h.got.Assignment != nil {
				t.Errorf("%s = %+v", name, h)
			}
		}
		if len(sch.claims) != 0 {
			t.Errorf("claims of a worker that may not claim = %+v", sch.claims)
		}
	})
}

// TestUnitWorkerPollWakeOne checks that a run made ready while polls are
// held wakes the newest idle worker's poll alone, which claims it, and
// that a woken poll that finds no run goes on holding.
func TestUnitWorkerPollWakeOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{})
		sch := &scheduler{}
		f.svc.BindScheduler(sch)
		registered := func(at time.Time) (worker.Worker, accessapi.Bearer) {
			w, b := f.add()
			w.CreatedAt = at
			f.store.workers[w.ID] = w
			return w, b
		}
		now := time.Now()
		_, older := registered(now.Add(-time.Hour))
		_, newest := registered(now)
		_, busy := registered(now.Add(time.Hour))
		olderCh := f.poll(older, worker.PollInput{})
		newestCh := f.poll(newest, worker.PollInput{})
		busyCh := f.poll(busy, worker.PollInput{Busy: true})
		pending(t, newestCh)

		f.svc.WakeOne(f.pool.ID)
		pending(t, newestCh)
		<-time.After(time.Second)
		a := sch.wait()
		f.svc.WakeOne(kernel.NewULID())
		f.svc.WakeOne(f.pool.ID)
		if h := <-newestCh; !claimed(h, a) || h.took != time.Second {
			t.Errorf("the newest poll = %+v", h)
		}
		pending(t, olderCh)
		for name, ch := range map[string]<-chan held{"older": olderCh, "busy": busyCh} {
			if h := <-ch; h.err != nil || h.got.Assignment != nil || h.took != worker.DefaultWait {
				t.Errorf("the %s poll = %+v", name, h)
			}
		}

		// Two workers that registered at once go by their IDs.
		first, firstB := registered(now)
		second, secondB := registered(now)
		if bytes.Compare(first.ID[:], second.ID[:]) > 0 {
			firstB, secondB = secondB, firstB
		}
		low, high := f.poll(firstB, worker.PollInput{}), f.poll(secondB, worker.PollInput{})
		pending(t, high)
		a = sch.wait()
		f.svc.WakeOne(f.pool.ID)
		if h := <-high; !claimed(h, a) {
			t.Errorf("the later ID's poll = %+v", h)
		}
		f.svc.Stop()
		if h := <-low; h.err != nil || h.got.Assignment != nil {
			t.Errorf("the earlier ID's poll = %+v", h)
		}
	})
}

// TestUnitWorkerPollClaimFails checks that a failing claim fails the
// poll, rolling back what it wrote, and that a worker gone before its
// claim is ErrUnauthenticated.
func TestUnitWorkerPollClaimFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, worker.Deps{Tokens: tokens{failRun: true}})
		sch := &scheduler{}
		f.svc.BindScheduler(sch)
		_, b := f.add()
		sch.wait()
		if h := <-f.poll(b, worker.PollInput{}); !errors.Is(h.err, errDown) {
			t.Errorf("run token down = %+v", h)
		}
		// Lock and Get fail before the claim, and Claimed after it.
		sch.wait()
		for _, method := range []string{"Lock", "Get", "Claimed"} {
			f.store.fail = method
			if h := <-f.poll(b, worker.PollInput{}); !errors.Is(h.err, errDown) {
				t.Errorf("%s down = %+v", method, h)
			}
		}
		f.store.fail = ""
		sch.err = errDown
		if h := <-f.poll(b, worker.PollInput{}); !errors.Is(h.err, errDown) {
			t.Errorf("claim down = %+v", h)
		}

		gone, gb := f.add()
		f.store.removeOnLock = gone.ID
		if h := <-f.poll(gb, worker.PollInput{}); !errors.Is(h.err, worker.ErrUnauthenticated) {
			t.Errorf("removed before the claim = %+v", h)
		}
	})
}
