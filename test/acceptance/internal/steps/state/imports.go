package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// importSteps are the steps of a state import: they put the state a run's
// tool pulled at an upload URL and check the version an import made, the
// upload it consumed and the trail it left.
//
// ref: decision 0010 § "Upload, import and download URLs"
type importSteps struct {
	w       *harness.World
	backend *backendSteps
	urls    *urlSteps
}

func (i *importSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's objectId names "(\w+)"$`, i.names)
	sc.Step(
		`^the last output of the run "(\w+)" is put with no credential at the URL "(\w+)" through ([AB])$`,
		i.put,
	)
	sc.Step(`^the upload of the URL "(\w+)" is gone from the uploads store$`, i.gone)
	sc.Step(
		`^the current state version of the stack "(\w+)" is imported by the (home|member) key, `+
			`with the serial and lineage of the last output of the run "(\w+)"$`,
		i.importedState,
	)
	sc.Step(
		`^the current state version of the stack "(\w+)" is imported by the (home|member) key, `+
			`client-encrypted$`,
		i.importedEncrypted,
	)
	sc.Step(`^the stack "(\w+)" has (\d+) imported state versions?$`, i.count)
	sc.Step(`^the problem's detail names the run "(\w+)"$`, i.namesRun)
	sc.Step(`^the audit trail holds (\d+) imports? into the stack "(\w+)" by the (home|member) key$`,
		i.audited)
}

// names keeps the last answer's objectId as name, for a later body to
// name as {name}.
func (i *importSteps) names(name string) error {
	var a struct {
		ObjectID string `json:"objectId"`
	}
	last := i.w.Last()
	if json.Unmarshal(last.Body, &a) != nil || a.ObjectID == "" {
		return fmt.Errorf("%w: no objectId in %d %s", harness.ErrStep, last.Status, last.Body)
	}
	i.w.IDs[name] = a.ObjectID
	return nil
}

// put PUTs the state the run kept as run's tool last printed, as state
// pull prints it, to the URL kept as name, leaving the answer to the
// answer steps.
func (i *importSteps) put(ctx context.Context, run, name, replica string) error {
	tr, err := i.backend.run(run)
	if err != nil {
		return err
	}
	url, err := i.urls.at(name, replica)
	if err != nil {
		return err
	}
	_, err = i.urls.send(ctx, http.MethodPut, url, tr.stdout)
	return err
}

// gone checks that the upload URL kept as name has no live object left
// in the uploads store: its import deleted it.
func (i *importSteps) gone(ctx context.Context, name string) error {
	object, ok := i.urls.objects[name]
	if !ok || object == "" {
		return fmt.Errorf("%w: no object ID kept with %q", harness.ErrStep, name)
	}
	var n int
	if err := i.w.Row(ctx, `SELECT count(*) FROM blob_objects
		WHERE store = 'uploads' AND state = 'live' AND split_part(key, '/', 3) = $1`,
		[]any{object}, &n); err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("%w: %d live objects of %s in uploads", harness.ErrStep, n, object)
	}
	return nil
}

// imported is the current version of the home account's stack slug's
// default workspace, as an import records it.
type imported struct {
	imported, at, run, encrypted bool
	username                     string
	serial                       *int64
	lineage                      *string
}

// current reads the current version of the stack slug's default
// workspace, and checks it is imported by the tenant's API key, at a
// time, and written by no run.
func (i *importSteps) current(ctx context.Context, slug, tenant string) (imported, error) {
	var v imported
	if err := i.w.Row(ctx, `SELECT v.imported, coalesce(v.imported_username, ''),
			v.imported_at IS NOT NULL, v.run_id IS NOT NULL, v.client_encrypted, v.serial,
			v.lineage
		FROM stacks s
			JOIN state_workspaces w ON w.account_id = s.account_id AND w.stack_id = s.id
				AND w.name = ''
			JOIN managed_state_versions v
				ON v.account_id = w.account_id AND v.id = w.current_version_id
		WHERE s.account_id = $1 AND s.slug = $2`,
		[]any{harness.UUIDOf(harness.Home.Account), slug}, &v.imported, &v.username, &v.at,
		&v.run, &v.encrypted, &v.serial, &v.lineage); err != nil {
		return v, err
	}
	if !v.imported || v.username != harness.Tenants[tenant].Key || !v.at || v.run {
		return v, fmt.Errorf("%w: the current version is imported %t by %q, at a time %t, "+
			"by a run %t", harness.ErrStep, v.imported, v.username, v.at, v.run)
	}
	return v, nil
}

// importedState checks that the current version of the stack slug is
// the tenant's import of the state the run kept as run's tool last
// printed: a plain state with its serial and lineage.
func (i *importSteps) importedState(ctx context.Context, slug, tenant, run string) error {
	tr, err := i.backend.run(run)
	if err != nil {
		return err
	}
	var local struct {
		Serial  *int64 `json:"serial"`
		Lineage string `json:"lineage"`
	}
	if json.Unmarshal([]byte(tr.stdout), &local) != nil || local.Serial == nil {
		return fmt.Errorf("%w: the last output is no state: %s", harness.ErrStep, tr.stdout)
	}
	v, err := i.current(ctx, slug, tenant)
	if err != nil {
		return err
	}
	if v.encrypted || v.serial == nil || *v.serial != *local.Serial || v.lineage == nil ||
		*v.lineage != local.Lineage {
		return fmt.Errorf("%w: the imported version is client-encrypted %t, serial %v of %v; "+
			"the tool's %d of %s", harness.ErrStep, v.encrypted, v.serial, v.lineage,
			*local.Serial, local.Lineage)
	}
	return nil
}

// importedEncrypted checks that the current version of the stack slug is
// the tenant's import of a client-encrypted state, kept with no serial or
// lineage since the server never parses it.
func (i *importSteps) importedEncrypted(ctx context.Context, slug, tenant string) error {
	v, err := i.current(ctx, slug, tenant)
	if err != nil {
		return err
	}
	if !v.encrypted || v.serial != nil || v.lineage != nil {
		return fmt.Errorf("%w: the imported version is client-encrypted %t, serial %v of %v",
			harness.ErrStep, v.encrypted, v.serial, v.lineage)
	}
	return nil
}

// count checks that the stack slug has n imported versions.
func (i *importSteps) count(ctx context.Context, slug string, n int) error {
	var got int
	if err := i.w.Row(ctx, `SELECT count(*) FROM managed_state_versions v
		JOIN stacks s ON s.account_id = v.account_id AND s.id = v.stack_id
		WHERE s.account_id = $1 AND s.slug = $2 AND v.imported`,
		[]any{harness.UUIDOf(harness.Home.Account), slug}, &got); err != nil {
		return err
	}
	if got != n {
		return fmt.Errorf("%w: the stack has %d imported versions", harness.ErrStep, got)
	}
	return nil
}

// namesRun checks that the last answer's problem detail names the run
// kept as run.
func (i *importSteps) namesRun(run string) error {
	id, ok := i.w.IDs[run]
	if !ok {
		return fmt.Errorf("%w: no run %q", harness.ErrStep, run)
	}
	var p struct {
		Detail string `json:"detail"`
	}
	last := i.w.Last()
	if json.Unmarshal(last.Body, &p) != nil || !strings.Contains(p.Detail, id) {
		return fmt.Errorf("%w: the problem's detail does not name %s: %s", harness.ErrStep, id,
			last.Body)
	}
	return nil
}

// audited checks that the home account's audit trail holds n imports
// into the stack slug, each by the tenant's API key from the caller's
// address and naming the version it made.
func (i *importSteps) audited(ctx context.Context, n int, slug, tenant string) error {
	acct := harness.UUIDOf(harness.Home.Account)
	var stack kernel.ULID
	if err := i.w.Row(ctx, `SELECT id FROM stacks WHERE account_id = $1 AND slug = $2`,
		[]any{acct, slug}, &stack); err != nil {
		return err
	}
	var got int
	if err := i.w.Row(ctx, `SELECT count(*) FROM audit_trail_entries
		WHERE account_id = $1 AND action = 'stack.state_import' AND affected_type = 'STACK'
			AND affected_id = $2 AND actor_username = $3
			AND affected_metadata::jsonb->>'versionId' <> '' AND coalesce(remote_ip, '') <> ''`,
		[]any{acct, stack.String(), harness.Tenants[tenant].Key}, &got); err != nil {
		return err
	}
	if got != n {
		return fmt.Errorf("%w: the trail holds %d imports into %s", harness.ErrStep, got, slug)
	}
	return nil
}
