package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// stopper holds what a space writer holds, and stop and kill.
var stopper = append(slices.Clone(writer), accessapi.ActionRunStop,
	accessapi.ActionRunStopBlocking, accessapi.ActionRunKill, accessapi.ActionRunKillBlocking)

// deliver delivers worker's pending signal through s, in a transaction.
func (w *world) deliver(t *testing.T, s *schedule.Signals, worker kernel.ULID) workersapi.Signal {
	t.Helper()
	var sig workersapi.Signal
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		sig, err = s.Deliver(ctx, worker)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return sig
}

// refusedIn fails the test unless err is a *control.Refused of a run in
// state.
func refusedIn(t *testing.T, what string, err error, state kernel.RunState) {
	t.Helper()
	if r, ok := errors.AsType[*control.Refused](err); !ok || r.State != state {
		t.Errorf("%s = %v, want a refusal in %s", what, err, state)
	}
}

// TestAccStopKill stops a planning run and kills it once its stop is
// delivered: the poll's delivery answers each signal once, a repeated
// stop or kill is answered as it is, a stale state and a run in another
// state are refused with the run's state, and the killed container fails
// the run with the note "killed" in the killer's name.
func TestAccStopKill(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	a.held = stopper
	c := w.controller(a)
	sig := schedule.NewSignals(w.store, nil)
	live := schedule.NewLiveness(w.store, w.lc)
	ctx, p := t.Context(), w.alice()
	pool, worker := kernel.NewULID(), kernel.NewULID()
	run := w.working(t, kernel.RunTypeTracked, kernel.RunStatePlanning, &pool, worker, w.now)
	raw := run.String()

	if _, err := sig.Deliver(ctx, worker); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Errorf("Deliver outside a transaction = %v", err)
	}
	_, err := c.Kill(ctx, p, "network", raw, "", "")
	refusedIn(t, "Kill before a stop", err, kernel.RunStatePlanning)
	_, err = c.Stop(ctx, p, "network", raw, "", kernel.RunStateInitializing)
	refusedIn(t, "Stop naming INITIALIZING", err, kernel.RunStatePlanning)
	if s := w.deliver(t, sig, worker); s != "" {
		t.Errorf("a signal before any was asked: %q", s)
	}

	v, err := c.Stop(ctx, p, "network", raw, "enough", kernel.RunStatePlanning)
	if err != nil || v.Run.Stop == nil || v.Run.Stop.By != "alice" ||
		v.Run.Stop.Note != "enough" || !v.Run.Stop.At.Equal(w.now.Truncate(time.Microsecond)) ||
		!slices.Contains(v.Actions, accessapi.ActionRunKill) ||
		slices.Contains(v.Actions, accessapi.ActionRunStop) {
		t.Fatalf("Stop = %+v, %v", v, err)
	}
	for i, want := range []workersapi.Signal{workersapi.SignalStop, ""} {
		if s := w.deliver(t, sig, worker); s != want {
			t.Errorf("delivery %d = %q, want %q", i, s, want)
		}
	}
	if v, err = c.Stop(ctx, p, "network", raw, "again", ""); err != nil ||
		v.Run.Stop.Note != "enough" || v.Run.Stop.DeliveredAt == nil {
		t.Errorf("a second Stop = %+v, %v", v.Run.Stop, err)
	}
	if s := w.deliver(t, sig, worker); s != "" {
		t.Errorf("a second stop was delivered: %q", s)
	}

	if v, err = c.Kill(ctx, p, "network", raw, "", ""); err != nil || v.Run.Kill == nil ||
		len(v.Actions) != 1 || v.Actions[0] != accessapi.ActionRunPrioritizeSet {
		t.Fatalf("Kill = %+v, %v", v, err)
	}
	if _, err = c.Kill(ctx, p, "network", raw, "", ""); err != nil {
		t.Errorf("a second Kill = %v", err)
	}
	for i, want := range []workersapi.Signal{workersapi.SignalKill, ""} {
		if s := w.deliver(t, sig, worker); s != want {
			t.Errorf("kill delivery %d = %q, want %q", i, s, want)
		}
	}
	if err := w.in(t, func(ctx context.Context) error {
		_, err := live.Ended(ctx, worker, run, workersapi.Exit{
			Code: 137, Reason: workersapi.ExitKilled,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w.failedWith(t, run, schedule.NoteKilled)
	if steps := w.steps(t, run); len(steps) != 1 || steps[0].Username == nil ||
		*steps[0].Username != "alice" {
		t.Errorf("the kill's transition = %+v", steps)
	}
	_, err = c.Stop(ctx, p, "network", raw, "", "")
	refusedIn(t, "Stop of a failed run", err, kernel.RunStateFailed)
	_, err = c.Kill(ctx, p, "network", raw, "", "")
	refusedIn(t, "Kill of a failed run", err, kernel.RunStateFailed)
}

// TestAccStopKillDelivery checks that a stop the run's state no longer
// takes is not delivered, that a kill goes in place of a stop not yet
// delivered, and that the run that holds its stack's lock needs the
// blocking variants.
func TestAccStopKillDelivery(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	a.held = stopper
	c := w.controller(a)
	sig := schedule.NewSignals(w.store, nil)
	ctx, p := t.Context(), w.alice()
	pool, worker := kernel.NewULID(), kernel.NewULID()
	run := w.working(t, kernel.RunTypeTracked, kernel.RunStateInitializing, &pool, worker, w.now)
	w.takeLock(t, lifecycle.Run{ID: run, Stack: w.stack})

	a.held = writer
	_, err := c.Stop(ctx, p, "network", run.String(), "", "")
	if b, ok := errors.AsType[*control.Blocked](err); !ok ||
		b.Action != accessapi.ActionRunStopBlocking {
		t.Errorf("Stop of the blocking run without RUN_STOP_BLOCKING = %v", err)
	}
	a.held = stopper
	if _, err := c.Stop(ctx, p, "network", run.String(), "", ""); err != nil {
		t.Fatal(err)
	}
	a.held = append(slices.Clone(writer), accessapi.ActionRunKill)
	_, err = c.Kill(ctx, p, "network", run.String(), "", "")
	if b, ok := errors.AsType[*control.Blocked](err); !ok ||
		b.Action != accessapi.ActionRunKillBlocking {
		t.Errorf("Kill of the blocking run without RUN_KILL_BLOCKING = %v", err)
	}

	// The run moves on to applying before its stop is delivered.
	if _, err := w.db.Exec(ctx, `UPDATE runs SET state = 'APPLYING' WHERE id = $1`,
		run); err != nil {
		t.Fatal(err)
	}
	if s := w.deliver(t, sig, worker); s != "" {
		t.Errorf("a stop delivered while applying: %q", s)
	}
	a.held = stopper
	if _, err := c.Kill(ctx, p, "network", run.String(), "", kernel.RunStateApplying); err != nil {
		t.Fatal(err)
	}
	if s := w.deliver(t, sig, worker); s != workersapi.SignalKill {
		t.Errorf("the kill = %q", s)
	}
	var stopDelivered bool
	if err := w.db.QueryRow(ctx, `SELECT stop_delivered_at IS NOT NULL FROM runs WHERE id = $1`,
		run).Scan(&stopDelivered); err != nil || !stopDelivered {
		t.Errorf("the stop the kill went in place of is delivered: %v, %v", stopDelivered, err)
	}
	if s := w.deliver(t, sig, kernel.NewULID()); s != "" {
		t.Errorf("another worker's signal: %q", s)
	}
}
