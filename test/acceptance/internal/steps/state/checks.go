package state

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func (b *backendSteps) registerChecks(sc *godog.ScenarioContext) {
	sc.Step(`^the relay (holds|releases) the state backend's unlocks$`, b.unlocks)
	sc.Step(`^the relay holds (\d+) unlocks? of the state of the run "(\w+)"$`, b.heldUnlocks)
	sc.Step(
		`^the relay passed an? (GET|POST|LOCK|UNLOCK) of the state of the run "(\w+)" answered (\d+)$`,
		b.passed,
	)
	sc.Step(
		`^the relay passed a LOCK of the state of the run "(\w+)" answered 423 with the LockInfo of its exclusive lock$`,
		b.refused,
	)
	sc.Step(
		`^the state of the run "(\w+)" is held by the (shared|exclusive) locks of the runs "([^"]*)"$`,
		b.locked,
	)
	sc.Step(`^the run "(\w+)" wrote (\d+) state versions?$`, b.wrote)
	sc.Step(
		`^the current state version is the run "(\w+)"'s, with the serial and lineage of its last output$`,
		b.current,
	)
	sc.Step(`^no object in the scenario's bucket holds "([^"]+)", of at least (\d+) objects?$`,
		b.sealed)
}

// unlocks makes the relay hold the backend's UNLOCK calls from now on,
// or pass the held ones on.
func (b *backendSteps) unlocks(what string) error {
	if b.relay == nil {
		return fmt.Errorf("%w: no relay, since the worker took over no run", harness.ErrStep)
	}
	if what == "holds" {
		b.relay.HoldUnlocks()
	} else {
		b.relay.ReleaseUnlocks()
	}
	return nil
}

// heldUnlocks waits until the relay holds n UNLOCK calls of the state the
// run kept as run reaches.
func (b *backendSteps) heldUnlocks(ctx context.Context, n int, run string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, fmt.Sprintf("the relay to hold %d unlocks", n),
		func(context.Context) (bool, error) { return b.relay.HeldUnlocks(tr.path) == n, nil })
}

// passed checks that the relay passed a call of method to the state the
// run kept as run reaches that answered status.
func (b *backendSteps) passed(method, run string, status int) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	if slices.Contains(
		b.relay.Calls(),
		harness.Call{Method: method, Path: tr.path, Status: status},
	) {
		return nil
	}
	return fmt.Errorf("%w: the relay passed no %s %s answered %d: %+v", harness.ErrStep, method,
		tr.path, status, b.relay.Calls())
}

// refused waits until the relay has passed a LOCK of the state the run
// kept as run reaches that answered 423 with the LockInfo of the
// exclusive lock the run holds there, as the server stored it.
func (b *backendSteps) refused(ctx context.Context, run string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	var info string
	if err := b.w.Row(ctx, `SELECT info::text FROM state_locks
		WHERE run_id = $1 AND mode = 'exclusive'`,
		[]any{harness.UUIDOf(tr.id)}, &info); err != nil {
		return err
	}
	var want any
	if err := json.Unmarshal([]byte(info), &want); err != nil {
		return fmt.Errorf("%w: the stored LockInfo %s: %w", harness.ErrStep, info, err)
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "a LOCK refused with the exclusive lock's LockInfo",
		func(context.Context) (bool, error) {
			for _, f := range b.relay.Refusals() {
				var got any
				if f.Method == stateapi.MethodLock && f.Path == tr.path &&
					json.Unmarshal([]byte(f.Body), &got) == nil && reflect.DeepEqual(got, want) {
					return true, nil
				}
			}
			return false, nil
		})
}

// locked checks that the locks held on the workspace the run kept as run
// holds a lock on are of mode, and are those of the runs kept as runs,
// comma-separated.
func (b *backendSteps) locked(ctx context.Context, run, mode, runs string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	var got string
	if err := b.w.Row(ctx, `SELECT coalesce(string_agg(run_id::text || ' ' || mode, ','
			ORDER BY run_id::text || ' ' || mode), '')
		FROM state_locks
		WHERE (account_id, stack_id, workspace) IN (
			SELECT account_id, stack_id, workspace FROM state_locks WHERE run_id = $1)`,
		[]any{harness.UUIDOf(tr.id)}, &got); err != nil {
		return err
	}
	var want []string
	for name := range strings.SplitSeq(runs, ",") {
		want = append(want, harness.UUIDOf(b.w.IDs[strings.TrimSpace(name)])+" "+mode)
	}
	slices.Sort(want)
	if got != strings.Join(want, ",") {
		return fmt.Errorf("%w: the locks are %q, want %q", harness.ErrStep, got, want)
	}
	return nil
}

// wrote checks how many state versions the run kept as run wrote, whether
// the scenario's worker or a launcher ran it.
func (b *backendSteps) wrote(ctx context.Context, run string, n int) error {
	id, ok := b.w.IDs[run]
	if !ok {
		return fmt.Errorf("%w: no run %q", harness.ErrStep, run)
	}
	var got int
	if err := b.w.Row(ctx, `SELECT count(*) FROM managed_state_versions WHERE run_id = $1`,
		[]any{harness.UUIDOf(id)}, &got); err != nil {
		return err
	}
	if got != n {
		return fmt.Errorf("%w: the run wrote %d state versions", harness.ErrStep, got)
	}
	return nil
}

// current checks that the current version of the workspace the run kept
// as run wrote to is one the run wrote, with the serial and lineage of
// the state its tool's last output holds, as state pull prints it.
func (b *backendSteps) current(ctx context.Context, run string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	var local struct {
		Serial  *int64 `json:"serial"`
		Lineage string `json:"lineage"`
	}
	if json.Unmarshal([]byte(tr.stdout), &local) != nil || local.Serial == nil ||
		local.Lineage == "" {
		return fmt.Errorf("%w: the last output is no state: %s", harness.ErrStep, tr.stdout)
	}
	var (
		writer, lineage string
		serial          int64
	)
	if err := b.w.Row(ctx, `SELECT v.run_id::text, v.serial, v.lineage
		FROM state_workspaces s JOIN managed_state_versions v
			ON v.account_id = s.account_id AND v.id = s.current_version_id
		WHERE (s.account_id, s.stack_id, s.name) IN (
			SELECT account_id, stack_id, workspace FROM managed_state_versions WHERE run_id = $1)`,
		[]any{harness.UUIDOf(tr.id)}, &writer, &serial, &lineage); err != nil {
		return err
	}
	if writer != harness.UUIDOf(tr.id) || serial != *local.Serial || lineage != local.Lineage {
		return fmt.Errorf("%w: the current version is serial %d of %s by %s, the tool's %d of %s",
			harness.ErrStep, serial, lineage, writer, *local.Serial, local.Lineage)
	}
	return nil
}

// sealed checks that no object in the scenario's bucket holds secret,
// having read at least n, so a bucket the servers never wrote to fails.
func (b *backendSteps) sealed(ctx context.Context, secret string, n int) error {
	store, err := b.w.Suite.Deps.ObjectStore(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names @objectstore
	}
	keys, read, err := store.Containing(ctx, b.w.Bucket, []byte(secret))
	switch {
	case err != nil:
		return err //nolint:wrapcheck // names the bucket
	case len(keys) > 0:
		return fmt.Errorf("%w: the objects %q hold the secret", harness.ErrStep, keys)
	case read < n:
		return fmt.Errorf("%w: the bucket holds %d objects, want at least %d", harness.ErrStep,
			read, n)
	}
	return nil
}
