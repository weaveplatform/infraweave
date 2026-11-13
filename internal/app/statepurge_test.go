package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateassembly "github.com/weaveplatform/infraweave/internal/state/assembly"
)

// stateOrphans counts the stack IDs the state tables hold whose stack is
// gone, as the nightly orphan count does; the locks a test seeds name
// runs that never existed, so their held runs are not counted.
func stateOrphans(t *testing.T, q tx.Querier) int64 {
	t.Helper()
	counts, err := integrity.Count(t.Context(), q, keys(), held())
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, c := range counts {
		if c.Held.Entity == "stack" && (strings.HasPrefix(c.Held.Table, "state_") ||
			strings.HasPrefix(c.Held.Table, "managed_state_")) {
			n += c.Count
		}
	}
	return n
}

// TestAccStatePurge checks a stack's state purge as the server wires the
// stacks and state domains over PostgreSQL. A rolled-back delete enqueues
// no purge. A committed delete enqueues state.purge, which the orphan
// count sees until it runs; the purge then deletes the stack's
// workspaces, locks and versions and marks the versions' objects
// deleting, leaving another stack's, after which the count finds no state
// row. The account's purge deletes the rest.
func TestAccStatePurge(t *testing.T) {
	w := newHoldWorld(t)
	ctx := t.Context()
	stacks := map[string]kernel.ULID{}
	for _, name := range []string{"gone", "kept"} {
		space := "kept"
		if _, err := w.d.Stacks.API().StackCreate(w.admin, api.StackCreateRequestObject{
			Body: &api.StackCreateRequest{Input: api.StackInput{
				Name: name, Repository: "infra", Branch: "main", Space: &space,
			}},
		}); err != nil {
			t.Fatal(err)
		}
		var id kernel.ULID
		if err := w.q.QueryRow(ctx, `SELECT id FROM stacks WHERE account_id = $1 AND slug = $2`,
			w.acct, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		stacks[name] = id
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := w.q.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// seed gives stack two workspaces with a lock each and three versions
	// of the default one, each with its live object, and answers their
	// keys.
	seed := func(stack kernel.ULID) []string {
		exec(`INSERT INTO state_workspaces (account_id, stack_id, name)
			VALUES ($1, $2, ''), ($1, $2, 'blue')`, w.acct, stack)
		exec(`INSERT INTO state_locks (account_id, stack_id, workspace, lock_id, mode, run_id, info)
			VALUES ($1, $2, '', 'a', 'shared', $3, '{}'), ($1, $2, 'blue', 'b', 'exclusive', $3, '{}')`,
			w.acct, stack, kernel.NewULID())
		keys := make([]string, 0, 3)
		for range 3 {
			id := kernel.NewULID()
			key := w.acct.String() + "/" + stack.String() + "/default/" + id.String()
			exec(`INSERT INTO blob_objects (store, key, size, sha256, state)
				VALUES ('states', $1, 1, $2, 'live')`, key, make([]byte, 32))
			exec(`INSERT INTO managed_state_versions (account_id, id, stack_id, workspace,
				object_key, size, sha256, serial, lineage)
				VALUES ($1, $2, $3, '', $4, 1, $5, 1, 'l')`,
				w.acct, id, stack, key, make([]byte, 32))
			keys = append(keys, key)
		}
		return keys
	}
	goneKeys, keptKeys := seed(stacks["gone"]), seed(stacks["kept"])
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := w.q.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	rows := func(stack kernel.ULID) int {
		return count(`SELECT (SELECT count(*) FROM state_workspaces WHERE stack_id = $1)
			+ (SELECT count(*) FROM state_locks WHERE stack_id = $1)
			+ (SELECT count(*) FROM managed_state_versions WHERE stack_id = $1)`, stack)
	}
	deleting := func(keys []string) int {
		return count(`SELECT count(*) FROM blob_objects WHERE store = 'states'
			AND state = 'deleting' AND key = ANY($1)`, keys)
	}
	purges := func() int {
		return count(`SELECT count(*) FROM jobs WHERE kind = $1`, stateassembly.StackPurge.Name)
	}

	errRollback := errors.New("rolled back")
	err := tx.Do(ctx, w.q, tx.Options{}, func(ctx context.Context) error {
		if err := w.d.State.StackGuard().Deleting(ctx, stacksapi.Stack{
			ID: stacks["gone"], Account: w.acct,
		}); err != nil {
			return err //nolint:wrapcheck // the test reads it as it is
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) || purges() != 0 {
		t.Fatalf("a rolled-back delete = %v, enqueuing %d purges", err, purges())
	}

	if _, err := w.d.Stacks.API().StackDelete(w.admin,
		api.StackDeleteRequestObject{StackId: "gone"}); err != nil {
		t.Fatal(err)
	}
	if purges() != 1 || rows(stacks["gone"]) != 7 || stateOrphans(t, w.q) == 0 {
		t.Fatalf("after the delete: %d purges, %d rows, %d orphans", purges(),
			rows(stacks["gone"]), stateOrphans(t, w.q))
	}

	reg := queue.NewRegistry()
	for _, j := range w.d.State.Jobs() {
		if err := reg.Register(j.Kind, j.Handler, j.Options); err != nil {
			t.Fatal(err)
		}
	}
	cfg := queue.Defaults()
	cfg.PollInterval = 20 * time.Millisecond
	workers := queue.NewWorkers(queue.New(w.q), reg, cfg, queue.AllowAll{}, nil, discard(), "test")
	running, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { workers.Run(running) })
	t.Cleanup(func() {
		stop()
		wg.Wait()
	})
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for rows(stacks["gone"]) != 0 {
		select {
		case <-deadline:
			t.Fatalf("the purge left %d rows of the deleted stack", rows(stacks["gone"]))
		case <-tick.C:
		}
	}
	if deleting(goneKeys) != 3 || deleting(keptKeys) != 0 || rows(stacks["kept"]) != 7 ||
		stateOrphans(t, w.q) != 0 {
		t.Errorf("after the stack's purge: %d and %d objects deleting, %d rows kept, %d orphans",
			deleting(goneKeys), deleting(keptKeys), rows(stacks["kept"]), stateOrphans(t, w.q))
	}

	if err := w.d.State.Purge(ctx, w.acct); err != nil {
		t.Fatal(err)
	}
	if rows(stacks["kept"]) != 0 || deleting(keptKeys) != 3 {
		t.Errorf("after the account's purge: %d rows, %d objects deleting", rows(stacks["kept"]),
			deleting(keptKeys))
	}
}
