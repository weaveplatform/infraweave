package timers_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/timers"
)

type (
	db     struct{}
	fakeTx struct{ pgx.Tx }
)

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error                       { return nil }
func (fakeTx) Rollback(context.Context) error                     { return nil }

// store holds runs and applies cutoffs as the pg adapter's query does:
// a run of a cutoff's state and type whose creation, or state change, is
// at or before its time, oldest state change first.
type store struct {
	runs  map[kernel.ULID]*lifecycle.Run
	reads int
	err   error
}

func (s *store) Due(_ context.Context, cutoffs []timers.Cutoff, batch int) ([]lifecycle.Run, error) {
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	var out []lifecycle.Run
	for _, r := range s.runs {
		if slices.ContainsFunc(cutoffs, func(c timers.Cutoff) bool {
			since := r.StateChangedAt
			if c.Created {
				since = r.CreatedAt
			}
			return c.State == r.State && c.Type == r.Type && !since.After(c.At)
		}) {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b lifecycle.Run) int {
		return cmp.Or(
			a.StateChangedAt.Compare(b.StateChangedAt),
			strings.Compare(a.ID.String(), b.ID.String()),
		)
	})
	return out[:min(batch, len(out))], nil
}

// mover moves the store's runs as the lifecycle would, checking the edge,
// and keeps each request.
type mover struct {
	s    *store
	reqs []lifecycle.Request
	err  error
}

func (m *mover) Transition(_ context.Context, req lifecycle.Request) (lifecycle.Run, error) {
	if m.err != nil {
		return lifecycle.Run{}, m.err
	}
	r := m.s.runs[req.Run]
	if _, ok := machine.Allowed(r.Type, r.State, req.To, req.Driver); !ok ||
		r.Version != req.Version {
		return lifecycle.Run{}, errors.New("refused")
	}
	m.reqs = append(m.reqs, req)
	r.State, r.Version = req.To, r.Version+1
	return *r, nil
}

// world is a pass over a store whose clock reads now.
type world struct {
	now   time.Time
	store *store
	moves *mover
}

func newWorld() *world {
	s := &store{runs: map[kernel.ULID]*lifecycle.Run{}}
	return &world{
		now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), store: s, moves: &mover{s: s},
	}
}

// run adds a run of typ in state, created ago created and changed ago
// changed.
func (w *world) run(typ kernel.RunType, state kernel.RunState, created, changed time.Duration) kernel.ULID {
	id := kernel.NewULID()
	w.store.runs[id] = &lifecycle.Run{
		ID: id, Type: typ, State: state, Version: 3,
		CreatedAt: w.now.Add(-created), StateChangedAt: w.now.Add(-changed),
	}
	return id
}

func (w *world) pass(t *testing.T, s timers.Settings) (timers.Ended, error) {
	t.Helper()
	return timers.New(timers.Deps{
		DB: db{}, Store: w.store, Lifecycle: w.moves, Settings: s,
		Now: func() time.Time { return w.now },
	}).Pass(t.Context())
}

const day = 24 * time.Hour

// TestUnitRunTimers checks each rule at its boundary and a minute short of
// it: the state each ends a run in, the note naming the rule, and the rule
// on the move; that the 30-day rule goes before any shorter one; and that
// a tracked run queued behind its stack's lock, and a run on a worker,
// are no timer's.
func TestUnitRunTimers(t *testing.T) {
	w := newWorld()
	type want struct {
		to    kernel.RunState
		timer runsapi.Timer
		note  string
	}
	cases, twins := map[kernel.ULID]want{}, map[kernel.ULID]want{}
	// add adds a run due on wt's rule, and with twin one a minute short of
	// it on both clocks, which falls due on the same rule a minute on.
	add := func(typ kernel.RunType, state kernel.RunState, created, changed time.Duration, wt want, twin bool) {
		cases[w.run(typ, state, created, changed)] = wt
		if twin {
			twins[w.run(typ, state, created-time.Minute, changed-time.Minute)] = wt
		}
	}
	pickup := "not picked up within "
	add(kernel.RunTypeProposed, kernel.RunStateQueued, 2*day, 2*day,
		want{kernel.RunStateCanceled, runsapi.TimerPickupProposed, pickup + "2 days"}, true)
	add(kernel.RunTypeTesting, kernel.RunStateReady, 3*day, 2*day,
		want{kernel.RunStateCanceled, runsapi.TimerPickupProposed, pickup + "2 days"}, true)
	add(kernel.RunTypeTracked, kernel.RunStateReady, 8*day, 7*day,
		want{kernel.RunStateCanceled, runsapi.TimerPickupBlocking, pickup + "7 days"}, true)
	add(kernel.RunTypeTask, kernel.RunStateReady, 7*day, 7*day,
		want{kernel.RunStateCanceled, runsapi.TimerPickupBlocking, pickup + "7 days"}, true)
	add(kernel.RunTypeDestroy, kernel.RunStateReady, 7*day, 7*day,
		want{kernel.RunStateCanceled, runsapi.TimerPickupBlocking, pickup + "7 days"}, true)
	add(kernel.RunTypeTracked, kernel.RunStateUnconfirmed, 9*day, 7*day,
		want{kernel.RunStateDiscarded, runsapi.TimerUnconfirmed, "unconfirmed for 7 days"}, true)
	add(kernel.RunTypeTracked, kernel.RunStateConfirmed, 9*day, 7*day,
		want{kernel.RunStateFailed, runsapi.TimerConfirmed, "not applied within 7 days"}, true)
	add(kernel.RunTypeTracked, kernel.RunStateReplanRequested, 9*day, 7*day,
		want{kernel.RunStateFailed, runsapi.TimerConfirmed, "not applied within 7 days"}, true)
	month := "not ended within 30 days of its creation"
	add(kernel.RunTypeProposed, kernel.RunStatePendingReview, 30*day, day,
		want{kernel.RunStateCanceled, runsapi.TimerMaxAge, month}, true)
	add(kernel.RunTypeTracked, kernel.RunStateQueued, 30*day, 30*day,
		want{kernel.RunStateCanceled, runsapi.TimerMaxAge, month}, true)
	add(kernel.RunTypeTracked, kernel.RunStateUnconfirmed, 30*day, 6*day,
		want{kernel.RunStateDiscarded, runsapi.TimerMaxAge, month}, true)
	// Due on both rules, it ends on the 30-day rule's, which never notifies.
	add(kernel.RunTypeTracked, kernel.RunStateConfirmed, 30*day, 8*day,
		want{kernel.RunStateFailed, runsapi.TimerMaxAge, month}, false)
	stay := map[kernel.ULID]string{
		w.run(kernel.RunTypeTracked, kernel.RunStateQueued, 29*day, 29*day):   "a tracked run queued behind the lock",
		w.run(kernel.RunTypeTracked, kernel.RunStatePlanning, 40*day, 20*day): "a run on a worker",
		w.run(kernel.RunTypeTracked, kernel.RunStateFinished, 40*day, 20*day): "a finished run",
	}
	check := func(when string, wants map[kernel.ULID]want) {
		t.Helper()
		if len(w.moves.reqs) != len(wants) {
			t.Errorf("%s, %d moves, want %d", when, len(w.moves.reqs), len(wants))
		}
		for _, req := range w.moves.reqs {
			wt, ok := wants[req.Run]
			if !ok {
				t.Errorf("%s, moved %s: %+v", when, stay[req.Run], req)
				continue
			}
			if req.To != wt.to || req.Timer != wt.timer || req.Note != wt.note ||
				req.Driver != machine.DriverTimers || req.Actor != (lifecycle.Actor{}) {
				t.Errorf("%s, move %+v, want %+v", when, req, wt)
			}
		}
	}

	ended, err := w.pass(t, timers.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	check("at the boundaries", cases)
	if ended[runsapi.TimerMaxAge] != 4 || ended[runsapi.TimerPickupProposed] != 2 ||
		ended[runsapi.TimerPickupBlocking] != 3 || ended[runsapi.TimerUnconfirmed] != 1 ||
		ended[runsapi.TimerConfirmed] != 2 {
		t.Errorf("ended %v", ended)
	}

	w.now = w.now.Add(time.Minute)
	w.moves.reqs = nil
	if _, err := w.pass(t, timers.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	check("a minute on", twins)
}

// TestUnitRunTimersBatches checks that a pass ends at most Batch runs a
// transaction and Batches transactions, oldest state change first, and
// stops at the first batch that is short.
func TestUnitRunTimersBatches(t *testing.T) {
	w := newWorld()
	for i := range timers.Batch*timers.Batches + 5 {
		w.run(
			kernel.RunTypeProposed,
			kernel.RunStateReady,
			10*day,
			3*day+time.Duration(i)*time.Second,
		)
	}
	ended, err := w.pass(t, timers.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if n := ended[runsapi.TimerPickupProposed]; n != timers.Batch*timers.Batches ||
		w.store.reads != timers.Batches {
		t.Errorf("ended %d in %d reads", n, w.store.reads)
	}
	for _, r := range w.store.runs {
		if r.State == kernel.RunStateReady &&
			r.StateChangedAt.Before(
				w.now.Add(-3*day-time.Duration(timers.Batch*timers.Batches)*time.Second),
			) {
			t.Fatalf("an older run %s is left", r.StateChangedAt)
		}
	}
	w.store.reads = 0
	if ended, err = w.pass(t, timers.DefaultSettings()); err != nil ||
		ended[runsapi.TimerPickupProposed] != 5 || w.store.reads != 1 {
		t.Errorf("the next pass ended %v in %d reads: %v", ended, w.store.reads, err)
	}
}

// TestUnitRunTimersSettings checks the defaults, a note in each unit, and
// that a timer under a minute is refused.
func TestUnitRunTimersSettings(t *testing.T) {
	d := timers.DefaultSettings()
	if d.PickupProposed != 2*day || d.PickupBlocking != 7*day || d.Unconfirmed != 7*day ||
		d.Confirmed != 7*day || d.MaxAge != 30*day {
		t.Errorf("defaults %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Error(err)
	}
	short := d
	short.Confirmed = 30 * time.Second
	if err := short.Validate(); !errors.Is(err, timers.ErrSettings) ||
		!strings.Contains(err.Error(), "run.timers.confirmed 30s is under 1m0s") {
		t.Errorf("Validate = %v", err)
	}
	for _, tc := range []struct {
		after time.Duration
		note  string
	}{
		{day, "1 day"},
		{36 * time.Hour, "36 hours"},
		{time.Hour, "1 hour"},
		{90 * time.Minute, "90 minutes"},
		{time.Minute, "1 minute"},
		{61 * time.Second, "1m1s"},
	} {
		w := newWorld()
		w.run(kernel.RunTypeTracked, kernel.RunStateUnconfirmed, tc.after, tc.after)
		s := d
		s.Unconfirmed = tc.after
		if _, err := w.pass(t, s); err != nil || len(w.moves.reqs) != 1 ||
			w.moves.reqs[0].Note != "unconfirmed for "+tc.note {
			t.Errorf("%s: %+v, %v", tc.after, w.moves.reqs, err)
		}
	}
}

// TestUnitRunTimersErrors checks that a store's error and a lifecycle's
// end the pass with them, and that a run no rule times out is refused.
func TestUnitRunTimersErrors(t *testing.T) {
	boom := errors.New("boom")
	w := newWorld()
	w.store.err = boom
	if _, err := w.pass(t, timers.DefaultSettings()); !errors.Is(err, boom) {
		t.Errorf("a store's error: %v", err)
	}
	w = newWorld()
	w.run(kernel.RunTypeTracked, kernel.RunStateConfirmed, 8*day, 8*day)
	w.moves.err = boom
	if ended, err := w.pass(t, timers.DefaultSettings()); !errors.Is(err, boom) || len(ended) != 0 {
		t.Errorf("a lifecycle's error: %v, %v", ended, err)
	}
	w = newWorld()
	w.run(kernel.RunTypeTracked, kernel.RunStateConfirmed, day, day)
	s := timers.DefaultSettings()
	if _, err := timers.New(timers.Deps{
		DB: db{}, Store: lax{w.store}, Lifecycle: w.moves, Settings: s,
		Now: func() time.Time { return w.now },
	}).Pass(t.Context()); err == nil || !strings.Contains(err.Error(), "no rule times out") {
		t.Errorf("a run no rule times out: %v", err)
	}
}

// lax is a store that reads every run it holds, due or not.
type lax struct{ *store }

func (l lax) Due(context.Context, []timers.Cutoff, int) ([]lifecycle.Run, error) {
	out := make([]lifecycle.Run, 0, len(l.runs))
	for _, r := range l.runs {
		out = append(out, *r)
	}
	return out, nil
}
