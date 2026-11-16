package app

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// TestAccStateRollback checks a rollback as the server wires the access,
// stacks, runs and state domains over PostgreSQL and an fs bucket: a
// stack created from one upload and then given a second, rolled back to
// the first, answers a new current version marked as the caller's
// rollback that keeps the first's import, resealed into an object of its
// own whose outputs are the first's; the stack answers isStateRollback
// until a newer version is written; the version search lists the
// versions newest first and filters the rollback; and a rollback to the
// current version, to one the stack lacks, or of an unlocked stack is
// refused, writing nothing.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestAccStateRollback(t *testing.T) {
	w := newImportWorld(t)
	w.d.Stacks.BindStateRollbacks(w.s.state.StateRollbacks())
	ctx := t.Context()
	p, _ := kernel.PrincipalFrom(w.admin)
	one, two := "s3cr3t-one-"+kernel.NewULID().String(), "s3cr3t-two-"+kernel.NewULID().String()
	if err := w.create(w.admin, "rolled", true, w.upload(t, w.admin, secretState(1, one))); err != nil {
		t.Fatal(err)
	}
	id := w.stackID(t, "rolled")
	first := w.latest(t, id)
	if _, err := w.d.Stacks.API().StackLock(w.admin, api.StackLockRequestObject{
		StackId: "rolled",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.importInto(w.admin, "rolled", w.upload(t, w.admin, secretState(2, two))); err != nil {
		t.Fatal(err)
	}
	second := w.latest(t, id)
	rollback := func(version string) (api.ManagedStateVersion, error) {
		res, err := w.s.state.API().StackManagedStateRollback(w.admin,
			api.StackManagedStateRollbackRequestObject{
				StackId: "rolled",
				Body: &api.StackManagedStateRollbackJSONRequestBody{
					ManagedStateVersionId: version,
				},
			})
		if err != nil {
			return api.ManagedStateVersion{}, err //nolint:wrapcheck // the test reads it as it is
		}
		return res.(api.StackManagedStateRollback200JSONResponse).Body, nil
	}
	versions := func(in api.SearchInput) []string {
		t.Helper()
		res, err := w.s.state.API().StackSearchManagedStateVersions(w.admin,
			api.StackSearchManagedStateVersionsRequestObject{
				StackId: "rolled", Params: api.StackSearchManagedStateVersionsParams{Input: in},
			})
		if err != nil {
			t.Fatal(err)
		}
		edges := res.(api.StackSearchManagedStateVersions200JSONResponse).Body.Edges
		out := make([]string, 0, len(edges))
		for _, e := range edges {
			out = append(out, e.Node.Id)
		}
		return out
	}
	rolledBack := func() bool {
		t.Helper()
		res, err := w.d.Stacks.API().Stack(w.admin, api.StackRequestObject{StackId: "rolled"})
		if err != nil {
			t.Fatal(err)
		}
		return res.(api.Stack200JSONResponse).Body.IsStateRollback
	}
	if rolledBack() {
		t.Error("a stack never rolled back answers isStateRollback")
	}

	v, err := rollback(first.ID.String())
	switch {
	case err != nil:
		t.Fatalf("a rollback to the first version = %v", err)
	case !v.Rollback || v.RollbackUsername == nil || *v.RollbackUsername != p.ActorID.String() ||
		!v.Imported || v.ImportedUsername == nil || *v.ImportedUsername != *first.ImportedUsername ||
		v.ImportedTimestamp == nil || int64(*v.ImportedTimestamp) != first.ImportedAt.Unix() ||
		v.Run != nil || v.Id == first.ID.String() || int64(v.Size) != first.Size ||
		v.LastModified == 0:
		t.Errorf("the rollback = %+v of %+v", v, first)
	}
	cur := w.latest(t, id)
	if cur.ID.String() != v.Id || cur.RolledBackFrom == nil || *cur.RolledBackFrom != first.ID ||
		*cur.Serial != 1 || !bytes.Equal(cur.SHA256, first.SHA256) {
		t.Errorf("the current version after the rollback = %+v", cur)
	}
	outs, err := w.s.state.Versions().Outputs(ctx, w.acct, id)
	if err != nil || len(outs) != 1 || string(outs[0].Value) != `"`+one+`"` {
		t.Errorf("the rollback's outputs = %+v, %v", outs, err)
	}
	var key string
	if err := w.q.QueryRow(ctx, `SELECT object_key FROM managed_state_versions
		WHERE account_id = $1 AND id = $2`, w.acct, cur.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	r, err := w.s.store.Get(ctx, objectstore.States, key)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(r)
	r.Close()
	if len(stored) == 0 || bytes.Contains(stored, []byte(one)) {
		t.Errorf("the rollback's object holds %d bytes, its plaintext among them", len(stored))
	}
	if !rolledBack() {
		t.Error("a rolled-back stack does not answer isStateRollback")
	}
	var audits int
	if err := w.q.QueryRow(ctx, `SELECT count(*) FROM audit_trail_entries
		WHERE account_id = $1 AND action = 'stack.state_rollback' AND affected_id = $2
			AND affected_metadata::jsonb->>'rolledBackFrom' = $3`,
		w.acct, id.String(), first.ID.String()).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("%d audit entries of the rollback, %v", audits, err)
	}

	if got := versions(api.SearchInput{}); strings.Join(got, ",") !=
		strings.Join([]string{v.Id, second.ID.String(), first.ID.String()}, ",") {
		t.Errorf("the versions newest first = %v", got)
	}
	rollbacks := []api.SearchQueryPredicate{{
		Field: "rollback", Constraint: api.SearchQueryFieldConstraint{BooleanEquals: &[]bool{true}},
	}}
	if got := versions(api.SearchInput{Predicates: &rollbacks}); len(got) != 1 || got[0] != v.Id {
		t.Errorf("the rollbacks = %v", got)
	}

	for name, tc := range map[string]struct {
		version string
		status  int
		detail  string
	}{
		"the current version":     {v.Id, http.StatusConflict, "current version"},
		"a version it lacks":      {kernel.NewULID().String(), http.StatusNotFound, "No state version"},
		"a version that is no ID": {"v1", http.StatusNotFound, "No state version"},
	} {
		if _, err := rollback(tc.version); status(err) != tc.status ||
			!strings.Contains(err.Error(), tc.detail) {
			t.Errorf("a rollback to %s = %v", name, err)
		}
	}
	if _, err := w.d.Stacks.API().StackUnlock(w.admin, api.StackUnlockRequestObject{
		StackId: "rolled",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rollback(second.ID.String()); status(err) != http.StatusConflict ||
		!strings.Contains(err.Error(), "not locked") {
		t.Errorf("a rollback of an unlocked stack = %v", err)
	}
	if w.latest(t, id).ID.String() != v.Id {
		t.Error("a refused rollback moved the current version")
	}

	if _, err := w.d.Stacks.API().StackLock(w.admin, api.StackLockRequestObject{
		StackId: "rolled",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.importInto(w.admin, "rolled", w.upload(t, w.admin, secretState(3, two))); err != nil {
		t.Fatal(err)
	}
	if rolledBack() {
		t.Error("a stack whose rollback an import followed answers isStateRollback")
	}
}
