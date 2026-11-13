package pg_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// unconfirmed is a tracked run of w's stack on hash that holds the
// stack's run lock and waits for confirmation, as a plan that changes
// something on a stack that does not deploy automatically leaves it.
func (w *world) unconfirmed(t *testing.T, hash string) lifecycle.Run {
	t.Helper()
	run := mustCreate(t, w, kernel.RunTypeTracked, hash)
	w.takeLock(t, run)
	run = w.mustMove(t, run, kernel.RunStateReady, machine.DriverScheduler,
		machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
		machine.GuardExternalSettled, machine.GuardStackEnabled)
	for _, m := range []struct {
		to kernel.RunState
		by machine.Driver
	}{
		{kernel.RunStatePreparing, machine.DriverWorkerClaim},
		{kernel.RunStateInitializing, machine.DriverWorkerReport},
		{kernel.RunStatePlanning, machine.DriverWorkerReport},
		{kernel.RunStateUnconfirmed, machine.DriverDecision},
	} {
		run = w.mustMove(t, run, m.to, m.by)
	}
	return run
}

// TestAccConfirm confirms the run named, which keeps the stack's run
// lock until it ends, and discards the run that holds the lock when none
// is named, which releases it; and refuses a run in another state, a
// stack whose lock no run holds, a run of another stack and one that
// does not exist.
func TestAccConfirm(t *testing.T) {
	w := newWorld(t)
	c := w.controller(w.access())
	ctx, p := t.Context(), w.alice()
	first := w.unconfirmed(t, "a")

	v, err := c.Confirm(ctx, p, "network", first.ID.String(), "looks good")
	if err != nil || v.Run.State != kernel.RunStateConfirmed {
		t.Fatalf("Confirm = %+v, %v", v.Run, err)
	}
	run := v.Run
	if last := v.History[len(v.History)-1]; last.State != kernel.RunStateConfirmed ||
		last.Username == nil || *last.Username != p.Login || last.Note == nil ||
		*last.Note != "looks good" {
		t.Errorf("CONFIRMED's transition = %+v", last)
	}
	if evs := w.updates(t, first.ID); len(evs) != 7 ||
		evs[6].Doc.State != kernel.RunStateConfirmed || evs[6].Doc.Username == nil ||
		*evs[6].Doc.Username != p.Login || evs[6].Doc.Note == nil ||
		*evs[6].Doc.Note != "looks good" {
		t.Errorf("%d run_updated events, last %+v", len(evs), evs[len(evs)-1].Doc)
	}
	if holder, held, err := w.store.Holder(ctx, w.acct, w.stack); err != nil || !held ||
		holder != first.ID {
		t.Errorf("the lock after the confirm: %s held %v, %v", holder, held, err)
	}
	for name, act := range map[string]func() (read.View, error){
		"Confirm": func() (read.View, error) { return c.Confirm(ctx, p, "network", "", "") },
		"Discard": func() (read.View, error) {
			return c.Discard(ctx, p, "network", first.ID.String(), "")
		},
	} {
		if _, err := act(); !errors.Is(err, control.ErrState) {
			t.Errorf("%s of a confirmed run = %v", name, err)
		}
	}

	for _, m := range []struct {
		to kernel.RunState
		by machine.Driver
	}{
		{kernel.RunStatePreparingApply, machine.DriverWorkerClaim},
		{kernel.RunStateApplying, machine.DriverWorkerReport},
		{kernel.RunStateFinished, machine.DriverWorkerReport},
	} {
		run = w.mustMove(t, run, m.to, m.by)
	}
	if _, err := c.Discard(ctx, p, "network", "", ""); !errors.Is(err, control.ErrNoHolder) {
		t.Errorf("Discard with no run named of an unlocked stack = %v", err)
	}

	second := w.unconfirmed(t, "b")
	if v, err = c.Discard(ctx, p, "network", "", "not this one"); err != nil ||
		v.Run.ID != second.ID || v.Run.State != kernel.RunStateDiscarded ||
		len(v.Actions) != 0 {
		t.Errorf("Discard of the run that holds the lock = %+v, %v", v.Run, err)
	}
	if _, held, err := w.store.Holder(ctx, w.acct, w.stack); err != nil || held {
		t.Errorf("the lock after the discard: held %v, %v", held, err)
	}

	w.stacks.settings[kernel.NewULID()] = stacksapi.Settings{Stack: stacksapi.Stack{Slug: "other"}}
	for _, c2 := range []struct{ slug, raw string }{
		{"other", second.ID.String()}, {"network", kernel.NewULID().String()}, {"network", "nope"},
	} {
		if _, err := c.Confirm(ctx, p, c2.slug, c2.raw, ""); !errors.Is(err, read.ErrNotFound) {
			t.Errorf("Confirm of %s on %s = %v", c2.raw, c2.slug, err)
		}
	}
	if _, err := c.Confirm(ctx, p, "gone", "", ""); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("Confirm on no stack = %v", err)
	}
	w.db.Close()
	if _, err := c.Discard(ctx, p, "network", "", ""); err == nil {
		t.Error("Discard on a closed pool succeeded")
	}
}
