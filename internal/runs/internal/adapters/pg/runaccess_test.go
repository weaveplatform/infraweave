package pg_test

import (
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestAccRunAccess reads a run's stack and the stack's space for its
// in-run API token, live only while it plans, applies or performs a task;
// answers a run the account lacks, or whose stack it lacks, as no
// subject; and passes the stacks domain's other failures on.
func TestAccRunAccess(t *testing.T) {
	w := newWorld(t)
	r := w.reader(w.access())
	ctx, worker, at := t.Context(), kernel.NewULID(), w.now
	space := w.stacks.settings[w.stack].Space
	for _, c := range []struct {
		typ   kernel.RunType
		state kernel.RunState
		live  bool
	}{
		{kernel.RunTypeTracked, kernel.RunStateInitializing, false},
		{kernel.RunTypeTracked, kernel.RunStatePlanning, true},
		{kernel.RunTypeTracked, kernel.RunStateUnconfirmed, false},
		{kernel.RunTypeTracked, kernel.RunStateApplying, true},
		{kernel.RunTypeTask, kernel.RunStatePerforming, true},
		{kernel.RunTypeDestroy, kernel.RunStateDestroying, false},
		{kernel.RunTypeTracked, kernel.RunStateFinished, false},
	} {
		run := w.working(t, c.typ, c.state, nil, worker, at)
		got, err := r.RunAccess(ctx, w.acct, run)
		if err != nil || got != (accessapi.RunAccess{Stack: w.stack, Space: space, Live: c.live}) {
			t.Errorf("RunAccess of a %s run in %s = %+v, %v", c.typ, c.state, got, err)
		}
	}

	run := w.working(t, kernel.RunTypeTracked, kernel.RunStatePlanning, nil, worker, at)
	other := kernel.AccountID{ULID: kernel.NewULID()}
	if _, err := r.RunAccess(ctx, other, run); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Errorf("RunAccess of another account's run = %v", err)
	}
	w.stacks.err["Get"] = stacksapi.ErrNotFound
	if _, err := r.RunAccess(ctx, w.acct, run); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Errorf("RunAccess of a run whose stack is gone = %v", err)
	}
	w.stacks.err["Get"] = errInjected
	if _, err := r.RunAccess(ctx, w.acct, run); !errors.Is(err, errInjected) {
		t.Errorf("RunAccess failing the stack's read = %v", err)
	}
	w.db.Close()
	if _, err := r.RunAccess(ctx, w.acct, run); err == nil ||
		errors.Is(err, accessapi.ErrNoSubject) {
		t.Errorf("RunAccess on a closed pool = %v", err)
	}
}
