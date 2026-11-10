package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// runTables are the runs domain's tables that name a stack, directly or
// through a run.
var runTables = map[string]string{
	"runs": `SELECT count(*) FROM runs WHERE stack_id = $1`,
	"run_state_transitions": `SELECT count(*) FROM run_state_transitions t JOIN runs r
		ON r.account_id = t.account_id AND r.id = t.run_id WHERE r.stack_id = $1`,
	"stack_run_locks": `SELECT count(*) FROM stack_run_locks WHERE stack_id = $1`,
	"stack_run_state": `SELECT count(*) FROM stack_run_state WHERE stack_id = $1`,
	"stack_outputs":   `SELECT count(*) FROM stack_outputs WHERE stack_id = $1`,
}

// TestAccRunsHoldRace checks the runs domain as the server wires it with
// stacks: a stack's view shows its state and blocked from the projection
// and its blocker from the runs domain, NONE and unblocked with no run. A
// stack delete that starts while a transaction holds the stack to write
// a run waits for it; when that transaction commits, the delete removes
// the run, its transition, the stack's run lock, tracked run and outputs
// with the stack, and when it rolls back, the delete goes ahead.
func TestAccRunsHoldRace(t *testing.T) {
	ctx := t.Context()
	w := newHoldWorld(t)
	d, q, acct := w.d, w.q, w.acct
	ids := map[string]kernel.ULID{}
	for _, name := range []string{"kept", "deleted", "rolled"} {
		space := "gone"
		res, err := d.Stacks.API().StackCreate(w.admin, api.StackCreateRequestObject{
			Body: &api.StackCreateRequest{Input: api.StackInput{
				Name: name, Repository: "infra", Branch: "main", Space: &space,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		slug := res.(api.StackCreate201JSONResponse).Body.Id
		var id kernel.ULID
		if err := q.QueryRow(ctx, `SELECT id FROM stacks WHERE account_id = $1 AND slug = $2`,
			acct, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	view := func(slug string) api.Stack {
		t.Helper()
		res, err := d.Stacks.API().Stack(w.admin, api.StackRequestObject{StackId: slug})
		if err != nil {
			t.Fatal(err)
		}
		return res.(api.Stack200JSONResponse).Body
	}
	if v := view("kept"); v.State != api.StackStateNONE || v.Blocked || v.Blocker != nil {
		t.Errorf("a stack with no run: %s, blocked %v, blocker %v", v.State, v.Blocked, v.Blocker)
	}

	// write writes, in the context's transaction, a tracked run of stack
	// that holds its run lock, with a transition and an output, as the
	// stack's applying state.
	write := func(ctx context.Context, stack kernel.ULID) (kernel.ULID, error) {
		run := kernel.NewULID()
		for _, s := range []string{
			`INSERT INTO runs (account_id, id, stack_id, type, state, commit, triggered_by)
				VALUES ($1, $2, $3, 'TRACKED', 'APPLYING', '{"hash":"abc"}', 'alice')`,
			`INSERT INTO run_state_transitions (account_id, run_id, sequence, state, terminal)
				SELECT account_id, id, 1, 'QUEUED', false FROM runs
				WHERE account_id = $1 AND id = $2 AND stack_id = $3`,
			`INSERT INTO stack_run_locks (account_id, stack_id, run_id) VALUES ($1, $3, $2)`,
			`INSERT INTO stack_run_state (account_id, stack_id, tracked_run_id) VALUES ($1, $3, $2)`,
			`INSERT INTO stack_outputs (account_id, stack_id, name, run_id, value)
				VALUES ($1, $3, 'url', $2, '"https://example.com"')`,
		} {
			if _, err := tx.Q(ctx, q).Exec(ctx, s, acct, run, stack); err != nil {
				return run, err //nolint:wrapcheck // the test reads it as it is
			}
		}
		//nolint:wrapcheck // as above
		return run, d.Stacks.Stacks().SetRunState(ctx, acct, stack,
			kernel.StackState(kernel.RunStateApplying), true)
	}
	var run kernel.ULID
	if err := tx.Do(ctx, q, tx.Options{}, func(ctx context.Context) error {
		if err := d.Stacks.Stacks().Hold(ctx, acct, ids["kept"]); err != nil {
			return err //nolint:wrapcheck // as above
		}
		var err error
		run, err = write(ctx, ids["kept"])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if v := view("kept"); v.State != api.StackStateAPPLYING || v.StateIsTerminal || !v.Blocked ||
		v.Blocker == nil || v.Blocker.Id != run.String() {
		t.Errorf("a stack an applying run blocks: %s, terminal %v, blocked %v, blocker %v",
			v.State, v.StateIsTerminal, v.Blocked, v.Blocker)
	}

	// race holds the stack slug, starts deleting it, and once the delete
	// waits, writes a run of the stack and commits, or rolls back; it
	// answers the hold's transaction's error and the delete's.
	race := func(slug string, commit bool) (held, deleted error) {
		holding, release := make(chan struct{}), make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			held = tx.Do(ctx, q, tx.Options{}, func(ctx context.Context) error {
				err := d.Stacks.Stacks().Hold(ctx, acct, ids[slug])
				close(holding)
				if err != nil {
					return err //nolint:wrapcheck // as above
				}
				<-release
				if !commit {
					return errRollback
				}
				_, err = write(ctx, ids[slug])
				return err
			})
		})
		<-holding
		wg.Go(func() {
			_, deleted = d.Stacks.API().StackDelete(w.admin,
				api.StackDeleteRequestObject{StackId: slug})
		})
		awaitLockWait(ctx, t, q)
		close(release)
		wg.Wait()
		return held, deleted
	}
	rows := func(slug string) map[string]int {
		out := map[string]int{}
		for table, query := range runTables {
			var n int
			if err := q.QueryRow(ctx, query, ids[slug]).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n > 0 {
				out[table] = n
			}
		}
		return out
	}

	if held, deleted := race("deleted", true); held != nil || deleted != nil {
		t.Fatalf("hold = %v, delete = %v", held, deleted)
	}
	if left := rows("deleted"); len(left) != 0 {
		t.Errorf("rows still name the deleted stack: %v", left)
	}
	if left := rows("kept"); len(left) != len(runTables) {
		t.Errorf("the kept stack's rows: %v, want one in each table", left)
	}

	held, deleted := race("rolled", false)
	if !errors.Is(held, errRollback) || deleted != nil {
		t.Errorf("after a rolled-back hold: hold = %v, delete = %v", held, deleted)
	}
	if left := rows("rolled"); len(left) != 0 {
		t.Errorf("rows name the rolled-back stack: %v", left)
	}
	var stacks int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM stacks WHERE account_id = $1
		AND id = ANY($2)`, acct, []kernel.ULID{ids["deleted"], ids["rolled"]}).Scan(&stacks); err != nil {
		t.Fatal(err)
	}
	if stacks != 0 {
		t.Errorf("%d deleted stacks are left", stacks)
	}
}
