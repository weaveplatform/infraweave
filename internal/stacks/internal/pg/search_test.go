package pg_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/sql/search/searchtest"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/stacks"
)

// FuzzStackSearch holds every search of the stack entity to
// parameterized SQL that keeps its rows to the reachable spaces.
func FuzzStackSearch(f *testing.F) {
	acct := kernel.NewAccountID()
	team := kernel.NewULID()
	searchtest.FuzzCompile(f, pg.StackSearch, pg.SearchScope(acct, stacks.Scope{
		Reachable: []kernel.ULID{acct.ULID, team},
		Spaces: []accessapi.SpaceNode{
			{ID: acct.ULID, Slug: "acme", Name: "Acme"},
			{ID: team, Slug: "team", Name: "Team", Parent: acct.ULID},
		},
		Actor: stacks.Actor{Kind: kernel.PrincipalAPIKey, ID: kernel.NewULID()},
	}))
}

// TestUnitStackFields holds stacks.Fields to the filters the reference's
// Terraform provider sends that this phase holds data for, and maps each
// field to SQL that answers it: a column for every field, a cast for every
// order, and the space tree for space.
func TestUnitStackFields(t *testing.T) {
	for _, name := range []string{"label", "name", "repository", "branch", "vendor", "locked"} {
		if _, ok := search.Lookup(stacks.Fields, name); !ok {
			t.Errorf("no field %q, which the reference's stack filters name", name)
		}
	}
	if len(pg.StackSearch.Columns) != len(stacks.Fields) {
		t.Fatalf("%d columns for %d fields", len(pg.StackSearch.Columns), len(stacks.Fields))
	}
	for _, c := range pg.StackSearch.Columns {
		switch {
		case c.Expr == "":
			t.Errorf("%s has no SQL", c.Name)
		case c.Orderable && c.Cast == "":
			t.Errorf("%s orders but has no cast for its cursor", c.Name)
		case (c.Type == search.Hierarchy) != c.SpaceTree:
			t.Errorf("%s: a %s field, SpaceTree %v", c.Name, c.Type, c.SpaceTree)
		case c.Type == search.Enum && len(c.Values) == 0:
			t.Errorf("%s is an enum of no values", c.Name)
		}
	}
	if v, _ := search.Lookup(stacks.Fields, "vendor"); !slices.Equal(
		v.Values, []string{"terraform", "opentofu"},
	) {
		t.Errorf("vendor's values = %v", v.Values)
	}
}

// TestAccServiceSearch searches and suggests over stacks in a tree of
// spaces: acme above team and hidden, team above child. The admin reads
// acme, team and child; the reader team alone; no one here hidden.
func TestAccServiceSearch(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	child := kernel.NewULID()
	f.spaces.ids["child"] = child
	f.spaces.parents = map[string]string{"child": "team"}
	all := f.authz[f.admin.Caller()][f.team]
	f.authz[f.admin.Caller()][child] = all
	owner := f.writer
	owner.ActorID = kernel.NewULID()
	f.authz[owner.Caller()] = map[kernel.ULID][]accessapi.Action{f.hidden: all}
	for _, c := range []struct {
		p                   kernel.Principal
		space, name, vendor string
		labels              []string
	}{
		{f.admin, "team", "Infra", "", []string{"prod", "eu"}},
		{f.admin, "child", "Web", "opentofu", []string{"prod"}},
		{f.admin, "acme", "Top", "", nil},
		{owner, "hidden", "Secret", "", []string{"prod"}},
	} {
		in := input(c.name)
		in.Space, in.Labels = c.space, c.labels
		if c.vendor != "" {
			in.VendorConfig = json.RawMessage(`{"` + c.vendor + `":{}}`)
		}
		if _, err := f.svc.Create(ctx, c.p, "", false, in); err != nil {
			t.Fatalf("create %s: %v", c.name, err)
		}
	}
	if _, err := f.svc.Lock(ctx, f.admin, "infra", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetStar(ctx, f.admin, "top", true); err != nil {
		t.Fatal(err)
	}
	find := func(p kernel.Principal, field string, exclude bool, c search.Constraint) []string {
		t.Helper()
		res, err := f.svc.Search(ctx, p, search.Input{Predicates: []search.Predicate{
			{Field: field, Exclude: exclude, Constraint: c},
		}})
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(res.Edges))
		for i, e := range res.Edges {
			out[i] = e.Node.Slug
		}
		return out
	}
	for name, c := range map[string]struct {
		p       kernel.Principal
		field   string
		exclude bool
		c       search.Constraint
		want    []string
	}{
		"a label": {
			f.admin, "label", false,
			search.Constraint{StringEquals: []string{"prod"}},
			[]string{"infra", "web"},
		},
		"no label": {
			f.admin, "label", true,
			search.Constraint{StringEquals: []string{"prod"}},
			[]string{"top"},
		},
		"team and below": {
			f.admin, "space", false,
			search.Constraint{Hierarchy: []search.Node{{Equals: "team", Descendants: true}}},
			[]string{"infra", "web"},
		},
		"team alone": {
			f.admin, "space", false,
			search.Constraint{Hierarchy: []search.Node{{Equals: "team"}}},
			[]string{"infra"},
		},
		"the whole tree": {
			f.admin, "space", false,
			search.Constraint{HierarchyValues: []string{"acme"}},
			[]string{"infra", "top", "web"},
		},
		"locked": {
			f.admin, "locked", false,
			search.Constraint{BooleanEquals: []bool{true}},
			[]string{"infra"},
		},
		"the admin's stars": {
			f.admin, "starred", false,
			search.Constraint{BooleanEquals: []bool{true}},
			[]string{"top"},
		},
		"the reader's stars": {
			f.reader, "starred", false,
			search.Constraint{BooleanEquals: []bool{true}},
			[]string{},
		},
		"a name": {
			f.admin, "name", false,
			search.Constraint{StringMatches: []string{"*EB"}},
			[]string{"web"},
		},
		"a vendor": {
			f.admin, "vendor", false,
			search.Constraint{EnumEquals: []string{"opentofu"}},
			[]string{"web"},
		},
		"the reader's label": {
			f.reader, "label", false,
			search.Constraint{StringEquals: []string{"prod"}},
			[]string{"infra"},
		},
	} {
		if got := find(c.p, c.field, c.exclude, c.c); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}

	page, err := f.svc.Search(ctx, f.admin, search.Input{
		First: new(int32(2)), OrderBy: []search.Order{{Field: "name", Desc: true}},
	})
	if err != nil || len(page.Edges) != 2 || page.Edges[0].Node.Slug != "web" || !page.HasNext {
		t.Fatalf("first page %+v, %v", page, err)
	}
	rest, err := f.svc.Search(ctx, f.admin, search.Input{
		First: new(int32(2)), After: page.End,
		OrderBy: []search.Order{{Field: "name", Desc: true}},
	})
	if err != nil || len(rest.Edges) != 1 || rest.Edges[0].Node.Slug != "infra" || rest.HasNext {
		t.Errorf("last page %+v, %v", rest, err)
	}
	if _, err := f.svc.Search(ctx, f.admin, search.Input{
		After: page.End, OrderBy: []search.Order{{Field: "repository"}},
	}); !isProblem(err, problem.InvalidCursor) {
		t.Errorf("a cursor of another order = %v", err)
	}
	if _, err := f.svc.Search(ctx, f.admin, search.Input{
		Predicates: []search.Predicate{{Field: "owner"}},
	}); !errors.As(err, new(*search.Invalid)) {
		t.Errorf("an unknown field = %v", err)
	}

	got, err := f.svc.Suggest(ctx, f.admin, search.SuggestionsInput{
		Fields: []string{"label", "space", "vendor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]search.Suggestion{}
	for _, sg := range got.Fields {
		by[sg.Field.Name] = sg
	}
	if got.Count != 3 || len(got.Fields) != len(stacks.Fields) ||
		!slices.Equal(by["label"].Values, []string{"prod", "eu"}) ||
		!slices.Equal(by["label"].Counts, []int{2, 1}) ||
		!slices.Equal(by["vendor"].Values, []string{"terraform", "opentofu"}) {
		t.Errorf("suggestions %+v", got)
	}
	nodes := []search.HierarchyNode{
		{ID: "acme", Display: "ACME"},
		{ID: "child", Parent: "team", Display: "CHILD"},
		{ID: "team", Parent: "acme", Display: "TEAM"},
	}
	if sp := by["space"]; !reflect.DeepEqual(sp.Nodes, nodes) ||
		!slices.Equal(sp.Counts, []int{3, 1, 2}) || !slices.Equal(sp.Direct, []int{1, 1, 1}) {
		t.Errorf("space = %+v", sp)
	}
	got, err = f.svc.Suggest(ctx, f.reader, search.SuggestionsInput{Fields: []string{"space"}})
	if err != nil || got.Count != 1 || !reflect.DeepEqual(got.Fields[4].Nodes,
		[]search.HierarchyNode{{ID: "team", Display: "TEAM"}}) {
		t.Errorf("the reader's suggestions %+v, %v", got, err)
	}
	if _, err := f.svc.Suggest(ctx, f.admin, search.SuggestionsInput{
		Predicates: []search.Predicate{{Field: "owner"}},
	}); !errors.As(err, new(*search.Invalid)) {
		t.Errorf("suggestions under an unknown field = %v", err)
	}

	f.spaces.treeErr = errors.New("access down")
	if _, err := f.svc.Search(ctx, f.admin, search.Input{}); !errors.Is(err, f.spaces.treeErr) {
		t.Errorf("Search without the tree = %v", err)
	}
	if _, err := f.svc.Suggest(
		ctx, f.admin, search.SuggestionsInput{},
	); !errors.Is(err, f.spaces.treeErr) {
		t.Errorf("Suggest without the tree = %v", err)
	}
}

func isProblem(err error, typ problem.Type) bool {
	pe, ok := errors.AsType[*problem.Error](err)
	return ok && pe.Problem.Status == typ.New("").Status && pe.Problem.Type == typ.New("").Type
}
