package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func (b *backendSteps) registerChecks(sc *godog.ScenarioContext) {
	sc.Step(`^the relay (holds|releases) the state backend's unlocks$`, b.unlocks)
	sc.Step(`^the relay holds (\d+) unlocks? of the state `+ofWorkspace+`of the run "(\w+)"$`,
		b.heldUnlocks)
	sc.Step(`^the relay (holds|releases) the state backend's writes$`, b.writes)
	sc.Step(`^the relay holds (\d+) writes? of the state `+ofWorkspace+`of the run "(\w+)"$`,
		b.heldWrites)
	sc.Step(
		`^the relay passed an? (GET|POST|LOCK|UNLOCK) of the state `+ofWorkspace+
			`of the run "(\w+)" answered (\d+)$`,
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
	sc.Step(`^the state workspaces of the run "(\w+)"'s stack are "([^"]*)", each at a `+
		`version the run wrote$`, b.workspaces)
	sc.Step(`^the unit state workspaces of the run "(\w+)"'s stack are "([^"]*)", each at a `+
		`version the run wrote$`, b.unitWorkspaces)
	sc.Step(
		`^the current state version is the run "(\w+)"'s, with the serial and lineage of its last output$`,
		b.current,
	)
	sc.Step(`^no object in the scenario's bucket holds "([^"]+)", of at least (\d+) objects?$`,
		b.sealed)
}

// ofWorkspace names a state workspace of a run's stack other than its
// default one, as "workspace "network/vpc" ", which a step's pattern
// puts between "the state " and "of the run"; absent, it is the default
// workspace.
const ofWorkspace = `(?:workspace "([^"]+)" )?`

// statePrefix is the path of a stack's state backend before the stack's
// ID, which a run's specification names as its stateBackend.path.
//
// ref: decision 0010 § "Endpoints"
const statePrefix = "/state/v1/stacks/"

// relayed is the relay the scenario's runs reach the state backend
// through: the worker's own once it took over a run, else the one the
// scenario's launchers reach the servers through.
func (b *backendSteps) relayed() (*harness.Relay, error) {
	switch {
	case b.relay != nil:
		return b.relay, nil
	case b.w.Relay != nil:
		return b.w.Relay, nil
	}
	return nil, fmt.Errorf("%w: no relay, since the worker took over no run and no launcher "+
		"started", harness.ErrStep)
}

// statePath is the path of the workspace ws, the default one when ws is
// empty, of the backend of the stack the run kept as run ran on, as the
// relay records it: decoded, so a / in the workspace's name, which the
// tool sends escaped as one segment, reads as a separator. A run the
// worker took over names its backend's path in its specification; one a
// launcher ran is found by its stack's ID.
func (b *backendSteps) statePath(ctx context.Context, run, ws string) (string, error) {
	path := ""
	if tr, ok := b.runs[run]; ok {
		path = tr.path
	} else {
		id, ok := b.w.IDs[run]
		if !ok {
			return "", fmt.Errorf("%w: no run %q", harness.ErrStep, run)
		}
		var stack kernel.ULID
		if err := b.w.Row(ctx, `SELECT stack_id::text FROM runs WHERE id = $1`,
			[]any{harness.UUIDOf(id)}, &stack); err != nil {
			return "", err //nolint:wrapcheck // names the query
		}
		path = statePrefix + stack.String()
	}
	if ws == "" || ws == "default" {
		return path, nil
	}
	return path + "/workspaces/" + ws, nil
}

// unlocks makes the relay hold the backend's UNLOCK calls from now on,
// or pass the held ones on.
func (b *backendSteps) unlocks(what string) error {
	relay, err := b.relayed()
	if err != nil {
		return err
	}
	if what == "holds" {
		relay.HoldUnlocks()
	} else {
		relay.ReleaseUnlocks()
	}
	return nil
}

// heldUnlocks waits until the relay holds n UNLOCK calls of the
// workspace ws of the state the run kept as run reaches.
func (b *backendSteps) heldUnlocks(ctx context.Context, n int, ws, run string) error {
	relay, err := b.relayed()
	if err != nil {
		return err
	}
	path, err := b.statePath(ctx, run, ws)
	if err != nil {
		return err
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, fmt.Sprintf("the relay to hold %d unlocks of %s", n, path),
		func(context.Context) (bool, error) { return relay.HeldUnlocks(path) == n, nil })
}

// writes makes the relay hold the backend's POST calls from now on, or
// pass the held ones on.
func (b *backendSteps) writes(what string) error {
	relay, err := b.relayed()
	if err != nil {
		return err
	}
	if what == "holds" {
		relay.HoldWrites()
	} else {
		relay.ReleaseWrites()
	}
	return nil
}

// heldWrites waits until the relay holds n POST calls of the workspace ws
// of the state the run kept as run reaches.
func (b *backendSteps) heldWrites(ctx context.Context, n int, ws, run string) error {
	relay, err := b.relayed()
	if err != nil {
		return err
	}
	path, err := b.statePath(ctx, run, ws)
	if err != nil {
		return err
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, fmt.Sprintf("the relay to hold %d writes of %s", n, path),
		func(context.Context) (bool, error) { return relay.HeldWrites(path) == n, nil })
}

// passed checks that the relay passed a call of method to the workspace
// ws of the state the run kept as run reaches that answered status.
func (b *backendSteps) passed(ctx context.Context, method, ws, run string, status int) error {
	relay, err := b.relayed()
	if err != nil {
		return err
	}
	path, err := b.statePath(ctx, run, ws)
	if err != nil {
		return err
	}
	if slices.Contains(relay.Calls(), harness.Call{Method: method, Path: path, Status: status}) {
		return nil
	}
	return fmt.Errorf("%w: the relay passed no %s %s answered %d: %+v", harness.ErrStep, method,
		path, status, relay.Calls())
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
// the scenario's worker or a launcher ran it. A rollback to one of them
// names the run too, but the run did not write it.
func (b *backendSteps) wrote(ctx context.Context, run string, n int) error {
	id, ok := b.w.IDs[run]
	if !ok {
		return fmt.Errorf("%w: no run %q", harness.ErrStep, run)
	}
	var got int
	if err := b.w.Row(ctx, `SELECT count(*) FROM managed_state_versions
		WHERE run_id = $1 AND NOT rollback`,
		[]any{harness.UUIDOf(id)}, &got); err != nil {
		return err
	}
	if got != n {
		return fmt.Errorf("%w: the run wrote %d state versions", harness.ErrStep, got)
	}
	return nil
}

// workspaces checks the state workspaces of the stack the run kept as run
// ran on, by name in order, comma-separated, default for the stack's
// default workspace, and that the current version of each is one the run
// wrote.
func (b *backendSteps) workspaces(ctx context.Context, run, names string) error {
	return b.checkWorkspaces(ctx, run, names, false)
}

// unitWorkspaces omits only an unwritten default workspace when no unit runs
// at the project root. A written default or any unexpected named unit fails.
func (b *backendSteps) unitWorkspaces(ctx context.Context, run, names string) error {
	return b.checkWorkspaces(ctx, run, names, true)
}

func (b *backendSteps) checkWorkspaces(ctx context.Context, run, names string, skipEmptyDefault bool) error {
	id, ok := b.w.IDs[run]
	if !ok {
		return fmt.Errorf("%w: no run %q", harness.ErrStep, run)
	}
	var got string
	if err := b.w.Row(ctx, `SELECT coalesce(string_agg(
			CASE WHEN s.name = '' THEN 'default' ELSE s.name END ||
			CASE WHEN v.run_id = $1 THEN '' ELSE ' (at a version of another run, or none)' END,
			', ' ORDER BY s.name), '')
		FROM state_workspaces s
		LEFT JOIN managed_state_versions v
			ON v.account_id = s.account_id AND v.id = s.current_version_id
		WHERE s.stack_id = (SELECT stack_id FROM runs WHERE id = $1)
		  AND (NOT $2 OR s.name <> '' OR s.current_version_id IS NOT NULL)`,
		[]any{harness.UUIDOf(id), skipEmptyDefault}, &got); err != nil {
		return err
	}
	if got != names {
		return fmt.Errorf("%w: the workspaces are %q", harness.ErrStep, got)
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

// sealed checks that no object in the scenario's bucket, or under its
// objects directory where its servers keep objects on the fs driver,
// holds secret, having read at least n, so a bucket the servers never
// wrote to fails.
func (b *backendSteps) sealed(ctx context.Context, secret string, n int) error {
	var (
		keys []string
		read int
		err  error
	)
	if slices.Contains(b.w.Tags, "@objectstore") {
		store, serr := b.w.Suite.Deps.ObjectStore(ctx)
		if serr != nil {
			return serr //nolint:wrapcheck // names @objectstore
		}
		keys, read, err = store.Containing(ctx, b.w.Bucket, []byte(secret))
	} else {
		keys, read, err = containing(b.w.Objects(), []byte(secret))
	}
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

// containing is the files under root that hold secret, and how many files
// it read.
func containing(root string, secret []byte) ([]string, int, error) {
	var keys []string
	read := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err //nolint:wrapcheck // names the path
		}
		read++
		if bytes.Contains(b, secret) {
			keys = append(keys, path)
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("the scenario's objects: %w", err)
	}
	return keys, read, nil
}
