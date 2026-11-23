package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
)

// TestAccStoreApplied reads whether a stack manages resources: not while
// no tracked run of it entered APPLYING, nor for a proposed run's or
// another stack's or account's apply, nor for a destroy run that did not
// finish; it does once a tracked run applied, no longer after a destroy
// run finished later, and again after a later apply.
func TestAccStoreApplied(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db, nil)
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	at := time.Now().Add(-time.Hour)
	enter := func(acct kernel.AccountID, stack kernel.ULID, typ kernel.RunType, state kernel.RunState) {
		t.Helper()
		id := kernel.NewULID()
		at = at.Add(time.Minute)
		if _, err := db.Exec(ctx, `INSERT INTO runs (account_id, id, stack_id, type, state,
			commit, triggered_by) VALUES ($1, $2, $3, $4, $5, '{}', 'alice')`,
			acct, id, stack, typ, state); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO run_state_transitions (account_id, run_id,
			sequence, state, timestamp, terminal) VALUES ($1, $2, 1, $3, $4, $5)`,
			acct, id, state, at, state.Terminal()); err != nil {
			t.Fatal(err)
		}
	}
	applied := func(want bool) {
		t.Helper()
		if got, err := s.Applied(ctx, acct, stack); err != nil || got != want {
			t.Errorf("Applied = %v, %v; want %v", got, err, want)
		}
	}

	applied(false)
	enter(acct, stack, kernel.RunTypeProposed, kernel.RunStateApplying)
	enter(acct, kernel.NewULID(), kernel.RunTypeTracked, kernel.RunStateApplying)
	enter(kernel.NewAccountID(), stack, kernel.RunTypeTracked, kernel.RunStateApplying)
	enter(acct, stack, kernel.RunTypeTracked, kernel.RunStatePlanning)
	applied(false)
	enter(acct, stack, kernel.RunTypeTracked, kernel.RunStateApplying)
	applied(true)
	enter(acct, stack, kernel.RunTypeDestroy, kernel.RunStateFailed)
	applied(true)
	enter(acct, stack, kernel.RunTypeDestroy, kernel.RunStateFinished)
	applied(false)
	enter(acct, stack, kernel.RunTypeTracked, kernel.RunStateApplying)
	applied(true)

	db.Close()
	if _, err := s.Applied(context.Background(), acct, stack); err == nil {
		t.Error("Applied on a closed pool succeeded")
	}
}
