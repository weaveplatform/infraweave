package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// rollbackSteps are the steps of a stack's state history: they search
// its versions, keep a version's ID for a rollback to name, and check the
// version a rollback answers and writes and the trail it leaves.
//
// ref: decision 0010 § "Rollback, searches and retention"
type rollbackSteps struct{ w *harness.World }

func (r *rollbackSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^token "(\w+)" searches the state versions of the stack "([^"]+)" with '([^']*)' `+
		`through ([AB])$`, r.search)
	sc.Step(`^the answer's state versions are "([^"]*)"$`, r.are)
	sc.Step(`^the answer's (\d+)(?:st|nd|rd|th) state version names "(\w+)"$`, r.names)
	sc.Step(`^the answer is a rollback by the (home|member) key to `+
		`(?:a version of the run "(\w+)"|an import by the (home|member) key)$`, r.answered)
	sc.Step(`^the current state version of the stack "(\w+)" is a rollback to "(\w+)"$`,
		r.current)
	sc.Step(`^the audit trail holds (\d+) rollbacks? of the stack "(\w+)" by the (home|member) key$`,
		r.audited)
}

// search sends stackSearchManagedStateVersions for the stack slug with
// the SearchInput raw, its {name}s resolved, as the input query parameter
// the operation reads it from.
func (r *rollbackSteps) search(ctx context.Context, token, slug, raw, replica string) error {
	path := "/api/v1/stacks/" + slug + "/search-managed-state-versions?input=" +
		url.QueryEscape(r.w.Resolve(raw))
	return r.w.SendAs(ctx, `token "`+token+`"`, http.MethodGet, path, "", replica)
}

// stateVersion is a state version as the API answers it.
type stateVersion struct {
	ID                string  `json:"id"`
	Size              int64   `json:"size"`
	LastModified      int64   `json:"lastModified"`
	Imported          bool    `json:"imported"`
	ImportedTimestamp *int64  `json:"importedTimestamp"`
	ImportedUsername  *string `json:"importedUsername"`
	Rollback          bool    `json:"rollback"`
	RollbackUsername  *string `json:"rollbackUsername"`
	Run               *struct {
		ID string `json:"id"`
	} `json:"run"`
}

// versions are the last answer's versions, a search's edges' nodes in
// order. Each must carry a ULID, a size and a time it was written, and
// an import or a rollback names who made it.
func (r *rollbackSteps) versions() ([]stateVersion, error) {
	last := r.w.Last()
	var body struct {
		Edges []struct {
			Node stateVersion `json:"node"`
		} `json:"edges"`
	}
	if last.Status != http.StatusOK || json.Unmarshal(last.Body, &body) != nil {
		return nil, fmt.Errorf("%w: no state versions: %d %s", harness.ErrStep, last.Status,
			last.Body)
	}
	out := make([]stateVersion, 0, len(body.Edges))
	for _, e := range body.Edges {
		if err := wellFormed(e.Node); err != nil {
			return nil, fmt.Errorf("%w: %s", err, last.Body)
		}
		out = append(out, e.Node)
	}
	return out, nil
}

// wellFormed checks the members every version answers.
func wellFormed(v stateVersion) error {
	if _, err := kernel.ParseULID(v.ID); err != nil || v.Size <= 0 || v.LastModified <= 0 ||
		v.Imported != (v.ImportedUsername != nil && v.ImportedTimestamp != nil) ||
		v.Rollback != (v.RollbackUsername != nil) {
		return fmt.Errorf("%w: a malformed state version %+v", harness.ErrStep, v)
	}
	return nil
}

// are checks the last answer's versions, in order, against want,
// comma-separated: each the name of the kept run that wrote it, or
// "imported" for an import, then "rolled back" for a rollback.
func (r *rollbackSteps) are(want string) error {
	vs, err := r.versions()
	if err != nil {
		return err
	}
	got := []string{}
	for _, v := range vs {
		entry := "imported"
		if v.Run != nil {
			entry = v.Run.ID
		}
		if v.Rollback {
			entry += " rolled back"
		}
		got = append(got, entry)
	}
	wanted := []string{}
	for entry := range strings.SplitSeq(want, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		name, rest, _ := strings.Cut(entry, " ")
		if name != "imported" {
			id, ok := r.w.IDs[name]
			if !ok {
				return fmt.Errorf("%w: no run %q", harness.ErrStep, name)
			}
			name = id
		}
		wanted = append(wanted, strings.TrimSpace(name+" "+rest))
	}
	if !slices.Equal(got, wanted) {
		return fmt.Errorf("%w: the state versions are %q, want %q", harness.ErrStep, got, wanted)
	}
	return nil
}

// names keeps the ID of the last answer's nth version, counting from 1,
// as name, for a later body to name as {name}.
func (r *rollbackSteps) names(n int, name string) error {
	vs, err := r.versions()
	if err != nil {
		return err
	}
	if n < 1 || n > len(vs) {
		return fmt.Errorf("%w: no version %d of %d", harness.ErrStep, n, len(vs))
	}
	r.w.IDs[name] = vs[n-1].ID
	return nil
}

// answered checks that the last answer is a version the tenant's API key
// rolled back, to a version the kept run wrote or, with run empty, to
// one the importer's API key imported, which keeps its import.
func (r *rollbackSteps) answered(tenant, run, importer string) error {
	last := r.w.Last()
	var v stateVersion
	if last.Status != http.StatusOK || json.Unmarshal(last.Body, &v) != nil {
		return fmt.Errorf("%w: no state version: %d %s", harness.ErrStep, last.Status, last.Body)
	}
	if err := wellFormed(v); err != nil {
		return err
	}
	if !v.Rollback || *v.RollbackUsername != harness.Tenants[tenant].Key {
		return fmt.Errorf("%w: not the %s key's rollback: %s", harness.ErrStep, tenant, last.Body)
	}
	if run != "" {
		if v.Run == nil || v.Run.ID != r.w.IDs[run] || v.Imported {
			return fmt.Errorf("%w: not a version of the run %s: %s", harness.ErrStep,
				r.w.IDs[run], last.Body)
		}
		return nil
	}
	if v.Run != nil || !v.Imported || *v.ImportedUsername != harness.Tenants[importer].Key {
		return fmt.Errorf("%w: not the %s key's import: %s", harness.ErrStep, importer, last.Body)
	}
	return nil
}

// current checks that the current version of the home account's stack
// slug's default workspace is a rollback to the version kept as target:
// the target's state, serial and lineage resealed into a live object of
// its own in the states store.
func (r *rollbackSteps) current(ctx context.Context, slug, target string) error {
	id, ok := r.w.IDs[target]
	if !ok {
		return fmt.Errorf("%w: no state version %q", harness.ErrStep, target)
	}
	if _, err := kernel.ParseULID(id); err != nil {
		return fmt.Errorf("%w: %q is no state version: %w", harness.ErrStep, id, err)
	}
	var same, live bool
	if err := r.w.Row(ctx, `SELECT
			c.sha256 = t.sha256 AND c.serial IS NOT DISTINCT FROM t.serial
				AND c.lineage IS NOT DISTINCT FROM t.lineage
				AND c.object_key <> t.object_key,
			EXISTS (SELECT 1 FROM blob_objects b
				WHERE b.store = 'states' AND b.key = c.object_key AND b.state = 'live')
		FROM stacks s
			JOIN state_workspaces w ON w.account_id = s.account_id AND w.stack_id = s.id
				AND w.name = ''
			JOIN managed_state_versions c
				ON c.account_id = w.account_id AND c.id = w.current_version_id
			JOIN managed_state_versions t ON t.account_id = c.account_id AND t.id = $3
		WHERE s.account_id = $1 AND s.slug = $2 AND c.rollback AND c.rolled_back_from = $3`,
		[]any{harness.UUIDOf(harness.Home.Account), slug, harness.UUIDOf(id)},
		&same, &live); err != nil {
		return err
	}
	if !same || !live {
		return fmt.Errorf("%w: the rollback keeps the target's state %t in a live object %t",
			harness.ErrStep, same, live)
	}
	return nil
}

// audited checks that the home account's audit trail holds n rollbacks
// of the stack slug, each by the tenant's API key from the caller's
// address and naming the version it made and the one it copied.
func (r *rollbackSteps) audited(ctx context.Context, n int, slug, tenant string) error {
	acct := harness.UUIDOf(harness.Home.Account)
	var stack kernel.ULID
	if err := r.w.Row(ctx, `SELECT id FROM stacks WHERE account_id = $1 AND slug = $2`,
		[]any{acct, slug}, &stack); err != nil {
		return err
	}
	var got int
	if err := r.w.Row(ctx, `SELECT count(*) FROM audit_trail_entries
		WHERE account_id = $1 AND action = 'stack.state_rollback' AND affected_type = 'STACK'
			AND affected_id = $2 AND actor_username = $3
			AND affected_metadata::jsonb->>'versionId' <> ''
			AND affected_metadata::jsonb->>'rolledBackFrom' <> ''
			AND coalesce(remote_ip, '') <> ''`,
		[]any{acct, stack.String(), harness.Tenants[tenant].Key}, &got); err != nil {
		return err
	}
	if got != n {
		return fmt.Errorf("%w: the trail holds %d rollbacks of %s", harness.ErrStep, got, slug)
	}
	return nil
}
