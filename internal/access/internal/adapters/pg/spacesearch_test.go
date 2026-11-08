package pg_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// TestAccSpaceSearch searches acme's tree of five spaces, acme, legacy,
// team, sub below team and other, with three of them reachable: pages
// are exact, the tree field keeps a node and its descendants, and
// suggestions name only reachable spaces. A search passed globex's legacy
// as reachable too still keeps acme's rows alone.
func TestAccSpaceSearch(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x, y := seed(t, db, "acme"), seed(t, db, "globex")
	tree := treeOf(x.s)
	team, err := tree.Create(
		ctx,
		x.acct,
		"acme",
		space.Space{Name: "Team", Labels: []string{"prod"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := tree.Create(ctx, x.acct, team.Slug, space.Space{Name: "Sub"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Create(ctx, x.acct, "acme", space.Space{Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := x.s.Space(ctx, x.acct, space.LegacySlug)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := y.s.Space(ctx, y.acct, space.LegacySlug)
	if err != nil {
		t.Fatal(err)
	}
	reach := []kernel.ULID{team.ID, sub.ID, legacy.ID, theirs.ID}
	find := func(in search.Input) search.Result[space.Space] {
		t.Helper()
		r, err := x.s.Search(ctx, x.acct, in, reach)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	slugs := func(r search.Result[space.Space]) []string {
		out := make([]string, 0, len(r.Edges))
		for _, e := range r.Edges {
			out = append(out, e.Node.Slug)
		}
		return out
	}

	first := find(search.Input{First: new(int32(2))})
	second := find(search.Input{First: new(int32(2)), After: first.End})
	if got := append(slugs(first), slugs(second)...); !slices.Equal(got,
		[]string{"legacy", sub.Slug, team.Slug}) || !first.HasNext || second.HasNext ||
		first.Edges[1].Node.ID != sub.ID {
		t.Errorf("pages = %v %+v %+v", got, first, second)
	}
	for name, c := range map[string]struct {
		pred search.Predicate
		want []string
	}{
		"below team": {search.Predicate{Field: "space", Constraint: search.Constraint{
			Hierarchy: []search.Node{{Equals: team.Slug, Descendants: true}},
		}}, []string{sub.Slug, team.Slug}},
		"this account's legacy": {search.Predicate{Field: "space", Constraint: search.Constraint{
			HierarchyValues: []string{"legacy"},
		}}, []string{"legacy"}},
		"below acme": {search.Predicate{Field: "space", Constraint: search.Constraint{
			HierarchyValues: []string{"acme"},
		}}, []string{"legacy", sub.Slug, team.Slug}},
		"parent": {search.Predicate{Field: "parentSpace", Constraint: search.Constraint{
			StringEquals: []string{team.Slug},
		}}, []string{sub.Slug}},
		"label": {search.Predicate{Field: "label", Constraint: search.Constraint{
			StringMatches: []string{"PR*"},
		}}, []string{team.Slug}},
		"inheriting": {search.Predicate{Field: "inheritEntities", Constraint: search.Constraint{
			BooleanEquals: []bool{true},
		}}, []string{}},
	} {
		if got := slugs(
			find(search.Input{Predicates: []search.Predicate{c.pred}}),
		); !slices.Equal(
			got,
			c.want,
		) {
			t.Errorf("%s = %v, want %v", name, got, c.want)
		}
	}
	byName := find(search.Input{
		OrderBy: []search.Order{{Field: "name", Desc: true}},
		Predicates: []search.Predicate{{Field: "space", Constraint: search.Constraint{
			HierarchyValues: []string{team.Slug},
		}}},
	})
	if got := slugs(byName); !slices.Equal(got, []string{team.Slug, sub.Slug}) {
		t.Errorf("below team by name, descending = %v", got)
	}
	if got := slugs(find(search.Input{FullText: "EGAC"})); !slices.Equal(got, []string{"legacy"}) {
		t.Errorf("containing egac = %v", got)
	}

	sg, err := x.s.Suggest(ctx, x.acct, search.SuggestionsInput{Fields: []string{"space", "label"}},
		reach[:3])
	if err != nil {
		t.Fatal(err)
	}
	f := map[string]search.Suggestion{}
	for _, s := range sg.Fields {
		f[s.Field.Name] = s
	}
	nodes := []search.HierarchyNode{
		{ID: "legacy", Display: "legacy"},
		{ID: sub.Slug, Parent: team.Slug, Display: "Sub"},
		{ID: team.Slug, Display: "Team"},
	}
	if sg.Count != 3 || !slices.Equal(f["space"].Nodes, nodes) ||
		!slices.Equal(f["space"].Counts, []int{1, 1, 2}) ||
		!slices.Equal(f["label"].Values, []string{"prod"}) || f["id"].Listed {
		t.Errorf("suggestions = %+v", sg)
	}

	_, err = x.s.Search(ctx, x.acct, search.Input{OrderBy: []search.Order{{Field: "label"}}}, reach)
	if _, ok := errors.AsType[*search.Invalid](err); !ok {
		t.Errorf("Search ordered by label = %v, want *search.Invalid", err)
	}
	_, err = x.s.Suggest(ctx, x.acct, search.SuggestionsInput{
		Predicates: []search.Predicate{{Field: "owner"}},
	}, reach)
	if _, ok := errors.AsType[*search.Invalid](err); !ok {
		t.Errorf("Suggest of an unknown field = %v, want *search.Invalid", err)
	}
}
