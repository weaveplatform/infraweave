package pg_test

import (
	"context"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
)

// TestAccStoreUnended reads a stack's runs that have not ended, oldest
// first, with their types and states: a run of every type in a state that
// has not ended, and none in a terminal state, another stack's or another
// account's.
func TestAccStoreUnended(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db, nil)
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	insert := func(acct kernel.AccountID, stack kernel.ULID, typ kernel.RunType, state kernel.RunState) runsapi.UnendedRun {
		t.Helper()
		r := runsapi.UnendedRun{ID: kernel.NewULID(), Type: typ, State: state}
		if _, err := db.Exec(ctx, `INSERT INTO runs (account_id, id, stack_id, type, state,
			commit, triggered_by, command) VALUES ($1, $2, $3, $4, $5, '{}', 'alice',
			CASE WHEN $4 = 'TASK' THEN 'ls' END)`, acct, r.ID, stack, typ, state); err != nil {
			t.Fatal(err)
		}
		return r
	}
	var want []runsapi.UnendedRun
	for i, st := range kernel.RunStates() {
		typ := kernel.RunTypes()[i%len(kernel.RunTypes())]
		r := insert(acct, stack, typ, st)
		if !st.Terminal() {
			want = append(want, r)
		}
	}
	insert(acct, kernel.NewULID(), kernel.RunTypeTracked, kernel.RunStateQueued)
	insert(kernel.NewAccountID(), stack, kernel.RunTypeTracked, kernel.RunStateQueued)

	got, err := s.Unended(ctx, acct, stack)
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Unended = %+v, %v; want %+v", got, err, want)
	}
	if got, err := s.Unended(ctx, acct, kernel.NewULID()); err != nil || len(got) != 0 {
		t.Errorf("Unended of a stack with no runs = %+v, %v", got, err)
	}

	db.Close()
	if _, err := s.Unended(context.Background(), acct, stack); err == nil {
		t.Error("Unended on a closed pool succeeded")
	}
}
