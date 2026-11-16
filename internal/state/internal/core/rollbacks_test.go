package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

func (m *mem) SearchVersions(_ context.Context, in search.Input, scope VersionScope) (search.Result[Version], error) {
	m.searched.in, m.searched.versions = in, scope
	var out search.Result[Version]
	for _, v := range m.versions {
		if v.Stack == scope.Stack && v.Workspace == scope.Workspace {
			out.Edges = append(out.Edges, search.Edge[Version]{Node: v})
		}
	}
	return out, m.failing("SearchVersions")
}

func (m *mem) RolledBack(_ context.Context, acct kernel.AccountID, stacks []kernel.ULID) ([]kernel.ULID, error) {
	m.searched.account = acct
	var out []kernel.ULID
	for _, id := range m.rolledBack {
		if slices.Contains(stacks, id) {
			out = append(out, id)
		}
	}
	return out, m.failing("RolledBack")
}

// history is an import's service whose stack's default workspace holds
// three versions, oldest first: one an import wrote, then one each of
// two runs, the last current, each with its live object.
type history struct {
	imports
	versions []Version
	bodies   []string
}

func newHistory() history {
	h := history{imports: newImports()}
	t := Target{Account: h.st.Account, Stack: h.st.ID}
	at := h.now.Add(-time.Hour)
	for i, body := range []string{
		`{"version":4,"serial":1,"lineage":"l-1","outputs":{"n":{"value":1}}}`,
		`{"version":4,"serial":2,"lineage":"l-1","outputs":{"n":{"value":2}}}`,
		`{"version":4,"serial":3,"lineage":"l-1","outputs":{}}`,
	} {
		sum := sha256.Sum256([]byte(body))
		v := Version{Version: stateapi.Version{
			ID: kernel.NewULID(), Stack: t.Stack, Size: int64(len(body)), SHA256: sum[:],
			Serial: new(int64(i + 1)), Lineage: new("l-1"),
			CreatedAt: at.Add(time.Duration(i) * time.Minute),
		}}
		if i == 0 {
			v.Imported, v.ImportedUsername, v.ImportedAt = true, new("carol"), &at
		} else {
			v.Run = new(kernel.NewULID())
		}
		v.Key = versionKey(t, v.ID)
		h.b.objects["states/"+v.Key], h.b.states["states/"+v.Key] = []byte(body), "live"
		h.m.workspaces[t] = true
		h.m.current[t], h.m.versions[v.ID] = v, v
		h.versions, h.bodies = append(h.versions, v), append(h.bodies, body)
	}
	return h
}

// rollback rolls st back to version as p from 192.0.2.1.
func (h history) rollback(t *testing.T, version string) (Version, error) {
	t.Helper()
	return h.s.Rollback(t.Context(), h.p, "192.0.2.1", "web", version)
}

// TestUnitRollback checks that a rollback writes a copy of an earlier
// version as the default workspace's new current version, under the
// stack's row lock with its runs read in the transaction: resealed into
// an object of its own, keeping the version's run, serial, lineage and
// import, marked as the caller's rollback from it, and recorded on the
// trail; and that a rollback to an imported version keeps who imported
// it and when.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestUnitRollback(t *testing.T) {
	h := newHistory()
	from := h.versions[1]
	v, err := h.rollback(t, from.ID.String())
	if err != nil {
		t.Fatalf("Rollback = %v", err)
	}
	cur, _ := h.current()
	switch {
	case !reflect.DeepEqual(v, cur) || v.ID == from.ID || len(h.m.versions) != 4:
		t.Errorf("the rollback answered %+v, made %+v current", v, cur)
	case !v.Rollback || v.RollbackUsername == nil || *v.RollbackUsername != "alice" ||
		v.RolledBackFrom == nil || *v.RolledBackFrom != from.ID:
		t.Errorf("the rollback's marks = %+v", v)
	case v.Run == nil || *v.Run != *from.Run || *v.Serial != 2 || *v.Lineage != "l-1" ||
		v.Imported || v.ImportedAt != nil || v.ClientEncrypted:
		t.Errorf("the rollback copied %+v of %+v", v, from)
	case v.Size != from.Size || string(v.SHA256) != string(from.SHA256) ||
		v.Key != versionKey(Target{Account: h.st.Account, Stack: h.st.ID}, v.ID) ||
		h.b.states["states/"+v.Key] != "live" || string(h.b.objects["states/"+v.Key]) != h.bodies[1]:
		t.Errorf("the rollback's object %s is %q, %q", v.Key, h.b.states["states/"+v.Key],
			h.b.objects["states/"+v.Key])
	case h.b.states["states/"+from.Key] != "live":
		t.Errorf("the target's object is %q", h.b.states["states/"+from.Key])
	}
	if !slices.Equal(h.m.lockedRun, []string{h.st.ID.String() + " in tx"}) ||
		!slices.Equal(h.runs.inTx, []bool{true}) {
		t.Errorf("locked %v, read the runs in a transaction %v", h.m.lockedRun, h.runs.inTx)
	}
	if len(h.m.audits) != 1 {
		t.Fatalf("audits = %+v", h.m.audits)
	}
	e := h.m.audits[0]
	var meta map[string]any
	_ = json.Unmarshal(e.AffectedResource.Metadata, &meta)
	if e.Action != ActionRollback || e.EventType != "UPDATE" || e.Actor.Username != "alice" ||
		e.AccountID != h.st.Account.ULID || e.AffectedResource.Type != ResourceStack ||
		e.AffectedResource.ID != h.st.ID.String() || e.RemoteIP != "192.0.2.1" ||
		meta["versionId"] != v.ID.String() || meta["rolledBackFrom"] != from.ID.String() ||
		meta["workspace"] != DefaultName {
		t.Errorf("the audit entry = %+v with metadata %v", e, meta)
	}

	imported := h.versions[0]
	h.p.Login = ""
	v, err = h.rollback(t, imported.ID.String())
	switch {
	case err != nil:
		t.Fatalf("a rollback to an imported version = %v", err)
	case !v.Imported || *v.ImportedUsername != "carol" || !v.ImportedAt.Equal(*imported.ImportedAt) ||
		v.Run != nil || !v.Rollback || *v.RollbackUsername != h.p.ActorID.String() ||
		*v.RolledBackFrom != imported.ID || *v.Serial != 1:
		t.Errorf("a rollback to an imported version = %+v", v)
	}
	if v, err = h.rollback(t, from.ID.String()); err != nil || *v.RolledBackFrom != from.ID {
		t.Errorf("a rollback to a version rolled back to before = %+v, %v", v, err)
	}
}

// TestUnitRollbackRefusals checks a rollback's refusals in order: a
// stack the account lacks; one that keeps no managed state, one
// unlocked, one another caller locked and one with a tracked run, task
// or destroy run under way, each a *Blocked worded for a rollback before
// the target is read; then a target the default workspace does not
// hold, ErrNoVersion, whether it names nothing, another stack's version,
// another workspace's or one whose object is gone; and last the current
// version, a *Blocked. None writes a version or an audit entry.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestUnitRollbackRefusals(t *testing.T) {
	h := newHistory()
	earlier := h.versions[0].ID.String()
	if _, err := h.s.Rollback(t.Context(), h.p, "", "api", earlier); !errors.Is(err,
		stacksapi.ErrNotFound) {
		t.Errorf("a rollback of a stack the account lacks = %v", err)
	}
	set := h.m.settings[h.st.ID]
	run := kernel.NewULID()
	for _, tc := range []struct {
		name   string
		change func(*stacksapi.Settings, *stackRuns)
		detail string
	}{
		{
			"no managed state", func(s *stacksapi.Settings, _ *stackRuns) { s.ManagesState = false },
			"not managed by infraweave",
		},
		{
			"no lock", func(s *stacksapi.Settings, _ *stackRuns) { s.Lock = nil },
			"not locked; lock it before rolling back its state",
		},
		{"another's lock", func(s *stacksapi.Settings, _ *stackRuns) {
			s.Lock = &stacksapi.Lock{Holder: "api_key:x", By: "bob"}
		}, "locked by bob; only the holder of its lock rolls back its state"},
		{"a tracked run", func(_ *stacksapi.Settings, r *stackRuns) {
			r.list = []runsapi.UnendedRun{{ID: run, Type: kernel.RunTypeTracked}}
		}, "TRACKED run " + run.String() + " has not finished; roll back its state once it has"},
		{"a task", func(_ *stacksapi.Settings, r *stackRuns) {
			r.list = []runsapi.UnendedRun{{ID: run, Type: kernel.RunTypeTask}}
		}, "TASK run"},
		{"a destroy run", func(_ *stacksapi.Settings, r *stackRuns) {
			r.list = []runsapi.UnendedRun{{ID: run, Type: kernel.RunTypeDestroy}}
		}, "DESTROY run"},
	} {
		s, r := set, &stackRuns{}
		tc.change(&s, r)
		h.m.settings[h.st.ID], h.runs.list = s, r.list
		// The target names no version, so a refusal before the target is
		// read is the only one this rollback can meet.
		_, err := h.rollback(t, kernel.NewULID().String())
		if b, ok := errors.AsType[*Blocked](err); !ok || !strings.Contains(b.Detail, tc.detail) {
			t.Errorf("%s = %v, want a *Blocked naming %q", tc.name, err, tc.detail)
		}
	}
	h.m.settings[h.st.ID], h.runs.list = set, nil

	other := Target{Account: h.st.Account, Stack: kernel.NewULID()}
	theirs := Version{Version: stateapi.Version{ID: kernel.NewULID(), Stack: other.Stack}}
	unit := Version{Version: stateapi.Version{
		ID: kernel.NewULID(), Stack: h.st.ID, Workspace: "modules/vpc",
	}}
	gone := h.versions[1]
	h.m.versions[theirs.ID], h.m.versions[unit.ID] = theirs, unit
	h.b.states["states/"+gone.Key] = "deleting"
	for name, raw := range map[string]string{
		"no ULID":                        "v1",
		"no version":                     kernel.NewULID().String(),
		"another stack's version":        theirs.ID.String(),
		"another workspace's":            unit.ID.String(),
		"a version whose object is gone": gone.ID.String(),
	} {
		if _, err := h.rollback(t, raw); !errors.Is(err, ErrNoVersion) {
			t.Errorf("a rollback to %s = %v, want ErrNoVersion", name, err)
		}
	}
	current := h.versions[2].ID
	_, err := h.rollback(t, current.String())
	if b, ok := errors.AsType[*Blocked](err); !ok ||
		!strings.Contains(b.Detail, current.String()+" is the stack's current version") {
		t.Errorf("a rollback to the current version = %v", err)
	}
	if cur, _ := h.current(); cur.ID != current || len(h.m.audits) != 0 ||
		len(h.b.byState()["pending"]) != 0 {
		t.Errorf("refusals left %s current, %d audit entries and pending objects %v", cur.ID,
			len(h.m.audits), h.b.byState()["pending"])
	}
}

// TestUnitRollbackDown checks that each read and write a rollback makes
// fails it with its error.
func TestUnitRollbackDown(t *testing.T) {
	for _, method := range []string{
		"BySlug", "LockForRun", "Unended", "Version", "LockWorkspace", "Current", "AddVersion",
		"Audit", "Open", "Put", "Commit",
	} {
		h := newHistory()
		h.m.fail, h.b.broken = method, method
		if method == "Unended" {
			h.runs.err = errDown
		}
		if _, err := h.rollback(t, h.versions[0].ID.String()); !errors.Is(err, errDown) {
			t.Errorf("a rollback whose %s fails = %v", method, err)
		}
	}
}

// TestUnitSearchVersions checks that a version search reads the default
// workspace of the caller's account's stack, in the stack's space, with
// the caller's input, and that a stack the account lacks or a store that
// fails answers its error.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestUnitSearchVersions(t *testing.T) {
	h := newHistory()
	h.st.Space = kernel.NewULID()
	h.m.stacks[0] = h.st
	first := int32(1)
	in := search.Input{First: &first, Predicates: []search.Predicate{{
		Field: "rollback", Constraint: search.Constraint{BooleanEquals: []bool{true}},
	}}}
	res, err := h.s.SearchVersions(t.Context(), h.p, "web", in)
	want := VersionScope{Target: Target{Account: h.st.Account, Stack: h.st.ID}, Space: h.st.Space}
	if err != nil || len(res.Edges) != 3 || !reflect.DeepEqual(h.m.searched.in, in) ||
		h.m.searched.versions != want {
		t.Errorf("SearchVersions = %+v, %v, reading %+v", res, err, h.m.searched.versions)
	}
	if _, err := h.s.SearchVersions(t.Context(), h.p, "api", in); !errors.Is(err,
		stacksapi.ErrNotFound) {
		t.Errorf("a search of a stack the account lacks = %v", err)
	}
	h.m.fail = "SearchVersions"
	if _, err := h.s.SearchVersions(t.Context(), h.p, "web", in); !errors.Is(err, errDown) {
		t.Errorf("a search whose store fails = %v", err)
	}
	for _, field := range []string{"createdAt", "run", "rollback", "imported"} {
		if _, ok := search.Lookup(VersionFields, field); !ok {
			t.Errorf("VersionFields lack %s", field)
		}
	}
	if f, _ := search.Lookup(VersionFields, "createdAt"); !f.Orderable || f.Type != search.Time {
		t.Errorf("createdAt = %+v, want an orderable time", f)
	}
}

// TestUnitRolledBack checks that the stacks the store finds rolled back
// answer true, and any other is absent.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestUnitRolledBack(t *testing.T) {
	m := newMem()
	s := New(Deps{Store: m})
	acct, back, plain := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	m.rolledBack = []kernel.ULID{back, kernel.NewULID()}
	got, err := s.RolledBack(t.Context(), acct, []kernel.ULID{back, plain})
	if err != nil || !reflect.DeepEqual(got, map[kernel.ULID]bool{back: true}) ||
		m.searched.account != acct {
		t.Errorf("RolledBack = %v, %v", got, err)
	}
	m.fail = "RolledBack"
	if _, err := s.RolledBack(t.Context(), acct, nil); !errors.Is(err, errDown) {
		t.Errorf("RolledBack with the store down = %v", err)
	}
}
