package pg_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql/search/searchtest"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
	"github.com/weaveplatform/infraweave/internal/state/internal/pg"
)

// FuzzVersionSearch holds every search of the version entity to
// parameterized SQL that keeps its rows to the stack's space.
func FuzzVersionSearch(f *testing.F) {
	searchtest.FuzzCompile(f, pg.VersionSearch, pg.VersionSearchScope(core.VersionScope{
		Target: core.Target{Account: kernel.NewAccountID(), Stack: kernel.NewULID()},
		Space:  kernel.NewULID(),
	}))
}

// TestUnitVersionFields maps each of core.VersionFields to SQL that
// answers it, with a cast for every order.
func TestUnitVersionFields(t *testing.T) {
	if len(pg.VersionSearch.Columns) != len(core.VersionFields) {
		t.Fatalf("%d columns for %d fields", len(pg.VersionSearch.Columns),
			len(core.VersionFields))
	}
	for _, c := range pg.VersionSearch.Columns {
		switch {
		case c.Expr == "":
			t.Errorf("%s has no SQL", c.Name)
		case c.Orderable && c.Cast == "":
			t.Errorf("%s orders but has no cast for its cursor", c.Name)
		}
	}
}

// TestAccStoreVersionSearch gives stack app's default workspace an
// import, then a version from each of runs one and two, then a rollback
// to the first run's, which keeps its run, and gives app's unit workspace
// and stack web a version each. A search through the core of app's
// versions sees its default workspace's alone, newest first unless it
// names an order, filters by rollback, imported, its run's ID as the API
// answers it, whole, matched in any case or in the full text, and when
// each was written, pages with a cursor, and refuses a field it lacks.
// The stack is rolled back until a version from a run follows the
// rollback.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestAccStoreVersionSearch(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	acct, space := kernel.NewAccountID(), kernel.NewULID()
	app := stacksapi.Stack{ID: kernel.NewULID(), Account: acct, Slug: "app", Space: space}
	web := stacksapi.Stack{ID: kernel.NewULID(), Account: acct, Slug: "web", Space: space}
	svc := core.New(core.Deps{DB: db, Store: s, Stacks: stacks{list: []stacksapi.Stack{app, web}}})
	p := kernel.Principal{Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: acct}
	sum := make([]byte, 32)
	one, two := kernel.NewULID(), kernel.NewULID()
	importedAt := time.Unix(1_800_000_000, 0).UTC()
	add := func(st stacksapi.Stack, workspace string, v stateapi.Version) core.Version {
		t.Helper()
		if err := s.OpenWorkspace(ctx, acct, st.ID, workspace); err != nil {
			t.Fatal(err)
		}
		v.ID, v.Stack, v.Workspace, v.SHA256, v.Serial, v.Lineage = kernel.NewULID(), st.ID,
			workspace, sum, new(int64(1)), new("l")
		cv := core.Version{Version: v, Key: v.ID.String()}
		if err := s.AddVersion(ctx, acct, cv); err != nil {
			t.Fatal(err)
		}
		return cv
	}
	imported := add(app, "", stateapi.Version{
		Size: 1, Imported: true, ImportedUsername: new("carol"), ImportedAt: &importedAt,
	})
	first := add(app, "", stateapi.Version{Size: 2, Run: &one})
	second := add(app, "", stateapi.Version{Size: 3, Run: &two})
	back := add(app, "", stateapi.Version{
		Size: 2, Run: &one, Rollback: true, RollbackUsername: new("dave"),
		RolledBackFrom: &first.ID,
	})
	add(app, "modules/vpc", stateapi.Version{Size: 4, Run: &one})
	add(web, "", stateapi.Version{Size: 5, Run: &one})

	find := func(in search.Input) search.Result[core.Version] {
		t.Helper()
		res, err := svc.SearchVersions(ctx, p, "app", in)
		if err != nil {
			t.Fatalf("SearchVersions(%+v) = %v", in, err)
		}
		return res
	}
	ids := func(res search.Result[core.Version]) []kernel.ULID {
		out := make([]kernel.ULID, len(res.Edges))
		for i, e := range res.Edges {
			out[i] = e.Node.ID
		}
		return out
	}
	where := func(field string, c search.Constraint) search.Input {
		return search.Input{Predicates: []search.Predicate{{Field: field, Constraint: c}}}
	}
	all := find(search.Input{})
	if got := ids(all); !slices.Equal(got, []kernel.ULID{back.ID, second.ID, first.ID, imported.ID}) {
		t.Fatalf("the versions newest first = %v", got)
	}
	v := all.Edges[0].Node
	if !v.Rollback || *v.RollbackUsername != "dave" || *v.RolledBackFrom != first.ID ||
		*v.Run != one || v.Size != 2 || v.Imported || v.CreatedAt.IsZero() {
		t.Errorf("the rollback reads back as %+v", v)
	}
	if v := all.Edges[3].Node; !v.Imported || *v.ImportedUsername != "carol" ||
		!v.ImportedAt.Equal(importedAt) || v.Run != nil {
		t.Errorf("the import reads back as %+v", v)
	}
	at := int32(time.Now().Add(time.Hour).Unix())
	for _, tc := range []struct {
		name string
		in   search.Input
		want []kernel.ULID
	}{
		{
			"rollbacks", where("rollback", search.Constraint{BooleanEquals: []bool{true}}),
			[]kernel.ULID{back.ID},
		},
		{
			"no rollbacks", where("rollback", search.Constraint{BooleanEquals: []bool{false}}),
			[]kernel.ULID{second.ID, first.ID, imported.ID},
		},
		{
			"imports", where("imported", search.Constraint{BooleanEquals: []bool{true}}),
			[]kernel.ULID{imported.ID},
		},
		{
			"run one", where("run", search.Constraint{StringEquals: []string{one.String()}}),
			[]kernel.ULID{back.ID, first.ID},
		},
		{"run two matched", where("run", search.Constraint{
			StringMatches: []string{"*" + strings.ToLower(two.String()[1:])},
		}), []kernel.ULID{second.ID}},
		{
			"run one's ID in the text",
			search.Input{FullText: one.String()},
			[]kernel.ULID{back.ID, first.ID},
		},
		{"no run", search.Input{Predicates: []search.Predicate{{
			Field: "run", Exclude: true, Constraint: search.Constraint{StringMatches: []string{"*"}},
		}}}, []kernel.ULID{imported.ID}},
		{"written later", where("createdAt", search.Constraint{
			TimeInRange: &search.TimeRange{Start: &at},
		}), []kernel.ULID{}},
		{
			"written lately", where("createdAt", search.Constraint{TimeInLast: "LAST1H"}),
			[]kernel.ULID{back.ID, second.ID, first.ID, imported.ID},
		},
		{
			"oldest first",
			search.Input{OrderBy: []search.Order{{Field: "createdAt"}}},
			[]kernel.ULID{imported.ID, first.ID, second.ID, back.ID},
		},
	} {
		if got := ids(find(tc.in)); !slices.Equal(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
	page := find(search.Input{First: new(int32(3))})
	rest := find(search.Input{First: new(int32(3)), After: page.End})
	if !page.HasNext || page.HasPrevious ||
		!slices.Equal(ids(rest), []kernel.ULID{imported.ID}) || rest.HasNext || !rest.HasPrevious {
		t.Errorf("paging = %v then %v", ids(page), ids(rest))
	}
	if _, err := svc.SearchVersions(ctx, p, "app", where("owner", search.Constraint{})); !errors.As(
		err, new(*search.Invalid)) {
		t.Errorf("a search of a field versions lack = %v", err)
	}
	if _, err := svc.SearchVersions(ctx, p, "gone", search.Input{}); !errors.Is(err,
		stacksapi.ErrNotFound) {
		t.Errorf("a search of a stack the account lacks = %v", err)
	}

	rolled, err := svc.RolledBack(ctx, acct, []kernel.ULID{app.ID, web.ID})
	if err != nil || len(rolled) != 1 || !rolled[app.ID] {
		t.Errorf("RolledBack = %v, %v", rolled, err)
	}
	if rolled, err := svc.RolledBack(ctx, kernel.NewAccountID(), []kernel.ULID{app.ID}); err != nil ||
		len(rolled) != 0 {
		t.Errorf("another account's RolledBack = %v, %v", rolled, err)
	}
	add(app, "", stateapi.Version{Size: 6, Run: &two})
	if rolled, err := svc.RolledBack(ctx, acct, []kernel.ULID{app.ID}); err != nil ||
		len(rolled) != 0 {
		t.Errorf("RolledBack after a run's version = %v, %v", rolled, err)
	}
}
