package pg_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// TestAccServicePoolUsers lists, counts, searches and suggests the stacks
// that select each pool: Infra names a private pool in team; Open in team
// and Top in the account's top space name none, so they select the public
// pool. The admin reads both spaces, the reader team alone, and another
// account's admin neither.
func TestAccServicePoolUsers(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	wp := kernel.NewULID()
	f.pools.reach[wp] = f.team
	for _, c := range []struct{ name, space string }{{"Infra", "team"}, {"Open", "team"}, {"Top", "acme"}} {
		in := input(c.name)
		in.Space = c.space
		if c.name == "Infra" {
			in.WorkerPool = new(wp.String())
		}
		if _, err := f.svc.Create(ctx, f.admin, "", false, "", in); err != nil {
			t.Fatalf("create %s: %v", c.name, err)
		}
	}
	stranger := f.admin
	stranger.AccountID = kernel.NewAccountID()

	got, err := f.svc.PoolUsers(ctx, f.admin, []kernel.ULID{wp, kernel.NewULID()})
	want := map[kernel.ULID][]workersapi.PoolUser{wp: {{Slug: "infra", Name: "Infra", Space: f.team}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("PoolUsers = %v, %v; want %v", got, err, want)
	}
	if got, err := f.svc.PoolUsers(ctx, f.admin, nil); err != nil || len(got) != 0 {
		t.Errorf("PoolUsers of no pool = %v, %v", got, err)
	}
	if got, err := f.svc.PoolUsers(ctx, stranger, []kernel.ULID{wp}); err != nil || len(got) != 0 {
		t.Errorf("another account's PoolUsers = %v, %v", got, err)
	}
	for _, c := range []struct {
		p    kernel.Principal
		want int
	}{{f.admin, 2}, {f.reader, 1}, {stranger, 0}} {
		if n, err := f.svc.PublicUsers(ctx, c.p); err != nil || n != c.want {
			t.Errorf("PublicUsers(%s) = %d, %v; want %d", c.p.Login, n, err, c.want)
		}
	}

	slugs := func(p kernel.Principal, u core.Users, in search.Input) []string {
		t.Helper()
		res, err := f.svc.SearchPoolUsers(ctx, p, u, in)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(res.Edges))
		for _, e := range res.Edges {
			out = append(out, e.Node.Slug)
		}
		return out
	}
	public, private := core.Users{}, core.Users{Pool: &wp}
	byName := search.Input{OrderBy: []search.Order{{Field: "name", Desc: true}}}
	for _, c := range []struct {
		what string
		got  []string
		want []string
	}{
		{"the private pool's users", slugs(f.admin, private, search.Input{}), []string{"infra"}},
		{"the public pool's users by name", slugs(f.admin, public, byName), []string{"top", "open"}},
		{"the public pool's users in team", slugs(f.reader, public, search.Input{}), []string{"open"}},
		{"the public pool's modules", slugs(f.admin, public, search.Input{Predicates: []search.Predicate{{
			Field: "isModule", Constraint: search.Constraint{BooleanEquals: []bool{true}},
		}}}), []string{}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}
	sug, err := f.svc.SuggestPoolUsers(ctx, f.admin, public, search.SuggestionsInput{Fields: []string{"name", "space"}})
	if err != nil || sug.Count != 2 || len(sug.Fields) != len(core.UserFields) {
		t.Errorf("SuggestPoolUsers = %+v, %v", sug, err)
	}
	var invalid *search.Invalid
	if _, err := f.svc.SearchPoolUsers(ctx, f.admin, public, search.Input{After: "nope"}); err == nil {
		t.Error("SearchPoolUsers after a cursor it never issued succeeded")
	}
	if _, err := f.svc.SuggestPoolUsers(ctx, f.admin, public, search.SuggestionsInput{
		Predicates: []search.Predicate{{Field: "colour", Constraint: search.Constraint{StringEquals: []string{"x"}}}},
	}); !errors.As(err, &invalid) {
		t.Errorf("SuggestPoolUsers of an unknown field = %v, want *search.Invalid", err)
	}
}
