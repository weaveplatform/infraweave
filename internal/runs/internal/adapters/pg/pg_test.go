package pg_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestMain migrates the platform stream and the runs stream alone: no
// runs table references another domain's.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// insertRun writes acct's run of stack with a transition, and, when
// tracked, makes it the stack's tracked run, run lock holder and the
// writer of an output.
func insertRun(
	t *testing.T,
	db tx.Querier,
	acct kernel.AccountID,
	stack kernel.ULID,
	tracked bool,
) kernel.ULID {
	t.Helper()
	ctx := t.Context()
	run := kernel.NewULID()
	stmts := []string{
		`INSERT INTO runs (account_id, id, stack_id, type, state, commit, triggered_by)
			VALUES ($1, $2, $3, 'TRACKED', 'APPLYING', '{"hash":"abc"}', 'alice')`,
		`INSERT INTO run_state_transitions (account_id, run_id, sequence, state, terminal)
			SELECT account_id, id, 1, 'QUEUED', false FROM runs
				WHERE account_id = $1 AND id = $2 AND stack_id = $3`,
	}
	if tracked {
		stmts = append(stmts,
			`INSERT INTO stack_run_locks (account_id, stack_id, run_id) VALUES ($1, $3, $2)`,
			`INSERT INTO stack_run_state (account_id, stack_id, tracked_run_id)
				VALUES ($1, $3, $2)`,
			`INSERT INTO stack_outputs (account_id, stack_id, name, run_id, value)
				VALUES ($1, $3, 'url', $2, '"https://example.com"')`,
		)
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s, acct, run, stack); err != nil {
			t.Fatal(err)
		}
	}
	return run
}

// count counts table's rows of acct.
func count(t *testing.T, db tx.Querier, table string, acct kernel.AccountID) int {
	t.Helper()
	var n int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM `+table+` WHERE account_id = $1`,
		acct).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var tables = []string{
	"runs", "run_state_transitions", "stack_run_locks", "stack_run_state", "stack_outputs",
}

// TestAccStore reads the run that holds each stack's run lock, none for
// another account's stack or an unlocked one, with the run's delta only
// while it waits in UNCONFIRMED, and deletes a stack's runs
// and every row of it in a transaction, keeping another stack's.
func TestAccStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db, nil)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	locked, idle, kept := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	run := insertRun(t, db, acct, locked, true)
	insertRun(t, db, acct, locked, false)
	insertRun(t, db, acct, idle, false)
	keptRun := insertRun(t, db, acct, kept, true)

	got, err := s.Blockers(ctx, acct, []kernel.ULID{locked, idle, kept})
	if err != nil || len(got) != 2 || got[locked] != (stacksapi.Blocker{Run: run}) ||
		got[kept] != (stacksapi.Blocker{Run: keptRun}) {
		t.Errorf("Blockers = %v, %v", got, err)
	}
	if _, err := db.Exec(ctx, `UPDATE runs SET delta = '{"add":3,"change":1,"delete":2,
		"import":1,"move":1,"forget":1,"resources":7,"outputs":true}'
		WHERE account_id = $1 AND id = ANY($2)`, acct, []kernel.ULID{run, keptRun}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE runs SET state = 'UNCONFIRMED'
		WHERE account_id = $1 AND id = $2`, acct, run); err != nil {
		t.Fatal(err)
	}
	got, err = s.Blockers(ctx, acct, []kernel.ULID{locked, kept})
	delta := kernel.PlanDelta{
		Add: 3, Change: 1, Delete: 2, Import: 1, Move: 1, Forget: 1, Resources: 7, Outputs: true,
	}
	if err != nil || got[locked].Delta == nil || *got[locked].Delta != delta ||
		got[kept].Delta != nil {
		t.Errorf("Blockers' deltas = %+v and %+v, %v", got[locked].Delta, got[kept].Delta, err)
	}
	if _, err := db.Exec(ctx, `UPDATE runs SET delta = '[]'
		WHERE account_id = $1 AND id = $2`, acct, run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Blockers(ctx, acct, []kernel.ULID{locked}); err == nil {
		t.Error("Blockers of a run whose delta is not an object succeeded")
	}
	if got, err := s.Blockers(ctx, other, []kernel.ULID{locked}); err != nil || len(got) != 0 {
		t.Errorf("Blockers of another account = %v, %v", got, err)
	}

	if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		return s.DeleteStack(ctx, acct, locked)
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"runs": 2, "run_state_transitions": 2, "stack_run_locks": 1, "stack_run_state": 1,
		"stack_outputs": 1,
	}
	for _, table := range tables {
		if n := count(t, db, table, acct); n != want[table] {
			t.Errorf("%d rows of %s after the delete, want %d", n, table, want[table])
		}
	}
	if got, err := s.Blockers(ctx, acct, []kernel.ULID{locked}); err != nil || len(got) != 0 {
		t.Errorf("Blockers of a deleted stack = %v, %v", got, err)
	}
}

// TestAccStoreStateAccess reads a run of every type by its ID alone, none
// for an unknown run, and answers the core's access from the state each
// Transition moves a tracked run to: a shared lock while it plans, writes
// under an exclusive lock while it applies, and nothing but its account
// once it finishes; another account's applying run never reaches the
// stack.
func TestAccStoreStateAccess(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	for _, typ := range kernel.RunTypes() {
		run := kernel.NewULID()
		if _, err := w.db.Exec(ctx, `INSERT INTO runs (account_id, id, stack_id, type, state,
			commit, triggered_by) VALUES ($1, $2, $3, $4, 'PLANNING', '{}', 'alice')`,
			w.acct, run, w.stack, typ); err != nil {
			t.Fatal(err)
		}
		want := core.Run{Account: w.acct, Stack: w.stack, State: kernel.RunStatePlanning}
		if got, ok, err := w.store.RunOf(ctx, run); err != nil || !ok || got != want {
			t.Errorf("RunOf a %s run = %+v, %t, %v", typ, got, ok, err)
		}
	}
	if got, ok, err := w.store.RunOf(ctx, kernel.NewULID()); err != nil || ok ||
		got != (core.Run{}) {
		t.Errorf("RunOf an unknown run = %+v, %t, %v", got, ok, err)
	}

	access := core.New(w.store)
	otherAcct := kernel.NewAccountID()
	other := insertRun(t, w.db, otherAcct, kernel.NewULID(), false)
	grant := func(run kernel.ULID) runsapi.StateGrant {
		t.Helper()
		g, err := access.Access(ctx, run, w.stack)
		if err != nil {
			t.Fatal(err)
		}
		if g, err := access.Access(ctx, other, w.stack); err != nil ||
			g != (runsapi.StateGrant{Account: otherAcct}) {
			t.Errorf("another account's applying run = %+v, %v", g, err)
		}
		return g
	}
	none := runsapi.StateGrant{Account: w.acct}
	run, _, err := w.create(t, kernel.RunTypeTracked, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if g := grant(run.ID); g != none {
		t.Errorf("QUEUED grants %+v", g)
	}
	run = w.mustMove(t, run, kernel.RunStateReady, machine.DriverScheduler,
		machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
		machine.GuardExternalSettled, machine.GuardStackEnabled)
	w.takeLock(t, run)
	for _, m := range []struct {
		to kernel.RunState
		by machine.Driver
	}{
		{kernel.RunStatePreparing, machine.DriverWorkerClaim},
		{kernel.RunStateInitializing, machine.DriverWorkerReport},
		{kernel.RunStatePlanning, machine.DriverWorkerReport},
	} {
		run = w.mustMove(t, run, m.to, m.by)
	}
	shared := runsapi.StateGrant{Account: w.acct, Read: true, Lock: runsapi.StateLockShared}
	if g := grant(run.ID); g != shared {
		t.Errorf("PLANNING grants %+v, want %+v", g, shared)
	}
	run = w.mustMove(t, run, kernel.RunStateUnconfirmed, machine.DriverDecision)
	if g := grant(run.ID); g != none {
		t.Errorf("UNCONFIRMED grants %+v", g)
	}
	run = w.mustMove(t, run, kernel.RunStateConfirmed, machine.DriverAPI, machine.GuardApproved)
	for _, m := range []struct {
		to kernel.RunState
		by machine.Driver
	}{
		{kernel.RunStatePreparingApply, machine.DriverWorkerClaim},
		{kernel.RunStateApplying, machine.DriverWorkerReport},
	} {
		run = w.mustMove(t, run, m.to, m.by)
	}
	exclusive := runsapi.StateGrant{
		Account: w.acct, Read: true, Lock: runsapi.StateLockExclusive, Write: true,
	}
	if g := grant(run.ID); g != exclusive {
		t.Errorf("APPLYING grants %+v, want %+v", g, exclusive)
	}
	run = w.mustMove(t, run, kernel.RunStateFinished, machine.DriverWorkerReport)
	if g := grant(run.ID); g != none {
		t.Errorf("FINISHED grants %+v", g)
	}
}

// TestAccStoreUnavailable fails each method on a closed pool.
func TestAccStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db, nil)
	db.Close()
	ctx := context.Background()
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	if _, err := s.Blockers(ctx, acct, []kernel.ULID{stack}); err == nil {
		t.Error("Blockers on a closed pool succeeded")
	}
	if err := s.DeleteStack(ctx, acct, stack); err == nil {
		t.Error("DeleteStack on a closed pool succeeded")
	}
	if _, _, err := s.RunOf(ctx, stack); err == nil {
		t.Error("RunOf on a closed pool succeeded")
	}
}

// TestUnitHeld declares a held stack column for each table that names a
// stack, and the runs' account, and the runs' key.
func TestUnitHeld(t *testing.T) {
	held := pg.Held()
	if len(held) != 5 || held[0].Entity != "account" || held[0].Table != "runs" {
		t.Fatalf("Held = %+v", held)
	}
	for i, table := range []string{"runs", "stack_run_locks", "stack_run_state", "stack_outputs"} {
		if h := held[i+1]; h.Entity != "stack" || h.Table != table {
			t.Errorf("held %d = %+v, want the stack of %s", i+1, h, table)
		}
	}
	if pg.Stream().Name != pg.StreamName {
		t.Errorf("the stream is named %q", pg.Stream().Name)
	}
	if k := pg.Keys(); len(k) != 1 || k[0].Entity != "run" || k[0].Table != "runs" {
		t.Errorf("Keys = %+v", k)
	}
}
