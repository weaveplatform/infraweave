package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql/search/searchtest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
	"github.com/weaveplatform/infraweave/internal/state/internal/pg"
)

// FuzzWorkspaceSearch holds every search of the workspace entity to
// parameterized SQL that keeps its rows to the reachable spaces.
func FuzzWorkspaceSearch(f *testing.F) {
	acct, space := kernel.NewAccountID(), kernel.NewULID()
	searchtest.FuzzCompile(f, pg.WorkspaceSearch, pg.WorkspaceSearchScope(acct, core.WorkspaceScope{
		Reachable: []kernel.ULID{space},
		Stacks:    []stacksapi.Stack{{ID: kernel.NewULID(), Account: acct, Slug: "app", Space: space}},
	}))
}

// TestUnitWorkspaceFields maps each of core.Fields to SQL that answers
// it, with a cast for every order.
func TestUnitWorkspaceFields(t *testing.T) {
	if len(pg.WorkspaceSearch.Columns) != len(core.Fields) {
		t.Fatalf("%d columns for %d fields", len(pg.WorkspaceSearch.Columns), len(core.Fields))
	}
	for _, c := range pg.WorkspaceSearch.Columns {
		switch {
		case c.Expr == "":
			t.Errorf("%s has no SQL", c.Name)
		case c.Orderable && c.Cast == "":
			t.Errorf("%s orders but has no cast for its cursor", c.Name)
		}
	}
}

// stacks is the stacks domain as a workspace search sees it: list holds
// every stack, each in its space.
type stacks struct {
	core.Stacks
	list []stacksapi.Stack
}

func (stacks) Hold(context.Context, kernel.AccountID, kernel.ULID) error { return nil }

func (s stacks) InSpaces(_ context.Context, acct kernel.AccountID, spaces []kernel.ULID) ([]stacksapi.Stack, error) {
	var out []stacksapi.Stack
	for _, st := range s.list {
		if st.Account == acct && slices.Contains(spaces, st.Space) {
			out = append(out, st)
		}
	}
	return out, nil
}

func (s stacks) BySlug(_ context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error) {
	for _, st := range s.list {
		if st.Account == acct && st.Slug == slug {
			return st, nil
		}
	}
	return stacksapi.Stack{}, stacksapi.ErrNotFound
}

// reader reads the spaces it holds.
type reader []kernel.ULID

func (r reader) Reachable(context.Context, kernel.Principal, accessapi.Action) ([]kernel.ULID, error) {
	return r, nil
}

// names are the names the edges' workspaces answer, in order.
func names(res search.Result[core.Workspace]) []string {
	out := make([]string, len(res.Edges))
	for i, e := range res.Edges {
		out[i] = e.Node.DisplayName()
	}
	return out
}

// TestAccStoreWorkspaces gives the stacks app and web, in a space the
// caller reads, and secret, in one it does not, workspaces through the
// core over the store: app's default held by two planning runs alice and
// bob triggered, its blue free and its green held by an applying run
// carol triggered, web's default free and secret's held by eve's. Each
// workspace keeps the ID its first lock gave it; a search, its paging and
// its suggestions see app's and web's alone, each answering who holds it;
// and releasing app's workspaces deletes their locks, auditing each.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestAccStoreWorkspaces(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	store := pg.New(db)
	acct, readable, hidden := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	app := stacksapi.Stack{ID: kernel.NewULID(), Account: acct, Slug: "app", Space: readable}
	web := stacksapi.Stack{ID: kernel.NewULID(), Account: acct, Slug: "web", Space: readable}
	secret := stacksapi.Stack{ID: kernel.NewULID(), Account: acct, Slug: "secret", Space: hidden}
	s := core.New(core.Deps{
		DB: db, Store: store, Stacks: stacks{list: []stacksapi.Stack{app, web, secret}},
		Authz: reader{readable},
	})
	lock := func(st stacksapi.Stack, name, id string, mode core.Mode, by string) {
		t.Helper()
		if err := s.Lock(ctx, core.Target{Account: acct, Stack: st.ID, Workspace: name}, core.Lock{
			ID: id, Mode: mode, Run: kernel.NewULID(), TriggeredBy: by, Info: info(id),
		}); err != nil {
			t.Fatalf("lock %s/%s: %v", st.Slug, name, err)
		}
	}
	lock(app, "", "a", core.Shared, "alice")
	lock(app, "", "b", core.Shared, "bob")
	lock(app, "green", "x", core.Exclusive, "carol")
	lock(secret, "", "e", core.Exclusive, "eve")
	for _, w := range []struct {
		st   stacksapi.Stack
		name string
	}{{app, "blue"}, {web, ""}} {
		if err := store.OpenWorkspace(ctx, acct, w.st.ID, w.name); err != nil {
			t.Fatal(err)
		}
	}

	id := func(st stacksapi.Stack, name string) kernel.ULID {
		t.Helper()
		var id kernel.ULID
		if err := db.QueryRow(ctx, `SELECT id FROM state_workspaces
			WHERE account_id = $1 AND stack_id = $2 AND name = $3`, acct, st.ID, name).
			Scan(&id); err != nil || id.IsZero() {
			t.Fatalf("the ID of %s/%q = %s, %v", st.Slug, name, id, err)
		}
		return id
	}
	def := id(app, "")
	if err := store.OpenWorkspace(ctx, acct, app.ID, ""); err != nil || id(app, "") != def {
		t.Errorf("a reopened workspace has the ID %s, want %s (%v)", id(app, ""), def, err)
	}
	for _, tc := range []struct {
		id     kernel.ULID
		name   string
		locked bool
		by     string
	}{
		{def, "", true, "alice"},
		{id(app, "green"), "green", true, "carol"},
		{id(app, "blue"), "blue", false, ""},
	} {
		w, ok, err := store.WorkspaceByID(ctx, acct, tc.id)
		if err != nil || !ok || w.ID != tc.id || w.Stack != app.ID || w.Name != tc.name ||
			w.Locked != tc.locked || w.TriggeredBy != tc.by || w.CreatedAt.IsZero() ||
			w.CreatedAt.Location() != time.UTC {
			t.Errorf("WorkspaceByID(%q) = %+v, %t, %v", tc.name, w, ok, err)
		}
	}
	for _, tc := range []struct {
		acct kernel.AccountID
		id   kernel.ULID
	}{{acct, kernel.NewULID()}, {kernel.NewAccountID(), def}} {
		if _, ok, err := store.WorkspaceByID(ctx, tc.acct, tc.id); ok || err != nil {
			t.Errorf("a workspace the account lacks = %t, %v", ok, err)
		}
	}

	p := kernel.Principal{Kind: kernel.PrincipalUser, AccountID: acct, Login: "dave"}
	find := func(in search.Input) search.Result[core.Workspace] {
		t.Helper()
		res, err := s.SearchWorkspaces(ctx, p, in)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	where := func(field string, c search.Constraint) search.Input {
		return search.Input{Predicates: []search.Predicate{{Field: field, Constraint: c}}}
	}
	all := find(search.Input{})
	if got := names(all); !slices.Equal(got, []string{"blue", "default", "default", "green"}) {
		t.Errorf("every workspace the caller reads, by name = %v", got)
	}
	for _, e := range all.Edges {
		if want := map[kernel.ULID]string{def: "alice", id(app, "green"): "carol"}[e.Node.ID]; e.Node.TriggeredBy != want ||
			e.Node.Locked != (want != "") {
			t.Errorf("%s answers locked %t by %q, want %q", e.Node.DisplayName(), e.Node.Locked,
				e.Node.TriggeredBy, want)
		}
	}
	for name, tc := range map[string]struct {
		in   search.Input
		want []string
	}{
		"the default name": {
			where("name", search.Constraint{StringEquals: []string{"default"}}),
			[]string{"default", "default"},
		},
		"a name pattern": {
			where("name", search.Constraint{StringMatches: []string{"GR*"}}), []string{"green"},
		},
		"a stack": {
			where("stack", search.Constraint{StringEquals: []string{"web"}}), []string{"default"},
		},
		"a hidden stack": {
			where("stack", search.Constraint{StringEquals: []string{"secret"}}), []string{},
		},
		"locked": {
			where("isLocked", search.Constraint{BooleanEquals: []bool{true}}),
			[]string{"default", "green"},
		},
		"free": {
			where("isLocked", search.Constraint{BooleanEquals: []bool{false}}),
			[]string{"blue", "default"},
		},
	} {
		if got := names(find(tc.in)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	if res := find(search.Input{
		OrderBy: []search.Order{{Field: "stack", Desc: true}},
	}); len(res.Edges) != 4 || res.Edges[0].Node.Stack != web.ID {
		t.Errorf("by stack, last first = %+v", res.Edges)
	}

	page := find(search.Input{First: new(int32(3))})
	rest := find(search.Input{First: new(int32(3)), After: page.End})
	if got := append(names(page), names(rest)...); !page.HasNext || rest.HasNext ||
		!slices.Equal(got, names(all)) {
		t.Errorf("two pages = %v, next %t then %t", got, page.HasNext, rest.HasNext)
	}
	if _, err := s.SearchWorkspaces(ctx, p, where("owner", search.Constraint{})); !errors.As(err,
		new(*search.Invalid)) {
		t.Errorf("an unknown field = %v", err)
	}

	got, err := s.SuggestWorkspaces(ctx, p, search.SuggestionsInput{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]search.Suggestion{}
	for _, sg := range got.Fields {
		by[sg.Field.Name] = sg
	}
	if got.Count != 4 || len(got.Fields) != len(core.Fields) || by["isLocked"].Listed ||
		!slices.Equal(by["name"].Values, []string{"default", "blue", "green"}) ||
		!slices.Equal(by["name"].Counts, []int{2, 1, 1}) ||
		!slices.Equal(by["stack"].Values, []string{"app", "web"}) ||
		!slices.Equal(by["stack"].Counts, []int{3, 1}) {
		t.Errorf("suggestions %+v", got)
	}
	got, err = s.SuggestWorkspaces(ctx, p, search.SuggestionsInput{
		Fields:     []string{"name"},
		Predicates: where("isLocked", search.Constraint{BooleanEquals: []bool{true}}).Predicates,
	})
	if err != nil || got.Count != 2 {
		t.Errorf("suggestions of the locked workspaces = %+v, %v", got, err)
	}

	order, err := func() ([]string, error) {
		var out []string
		err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
			ws, err := store.LockStackWorkspaces(ctx, acct, app.ID)
			for _, w := range ws {
				out = append(out, w.Name)
				if w.Locked {
					t.Errorf("%q answers locked before its locks are read", w.Name)
				}
			}
			return err //nolint:wrapcheck // the test reads it as it is
		})
		return out, err //nolint:wrapcheck // as above
	}()
	if err != nil || !slices.Equal(order, []string{"", "blue", "green"}) {
		t.Errorf("LockStackWorkspaces = %v, %v", order, err)
	}

	unlocked, err := s.UnlockStack(ctx, p, "192.0.2.9", "app")
	if err != nil || len(unlocked) != 2 || unlocked[0].ID != def || unlocked[1].Name != "green" {
		t.Errorf("UnlockStack = %+v, %v", unlocked, err)
	}
	var left, broken, entries int
	if err := db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM state_locks WHERE account_id = $1 AND stack_id = $2),
		(SELECT count(*) FROM state_broken_locks WHERE account_id = $1 AND stack_id = $2),
		(SELECT count(*) FROM audit_trail_entries WHERE account_id = $1
			AND action = $3 AND actor_username = 'dave' AND affected_id = $4
			AND remote_ip = '192.0.2.9')`,
		acct, app.ID, core.ActionBreakLock, app.ID.String()).Scan(&left, &broken,
		&entries); err != nil || left != 0 || broken != 3 || entries != 2 {
		t.Errorf("%d locks left, %d broken and %d entries, want none, three and two (%v)",
			left, broken, entries, err)
	}
	if res := find(where("isLocked", search.Constraint{BooleanEquals: []bool{true}})); len(res.Edges) != 0 {
		t.Errorf("%v locked after the release", names(res))
	}
}
