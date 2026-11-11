package pg_test

import (
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql/search/searchtest"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// FuzzRunSearch holds every search of the run entity to parameterized SQL
// that keeps its rows to the reachable spaces.
func FuzzRunSearch(f *testing.F) {
	acct := kernel.NewAccountID()
	team := kernel.NewULID()
	searchtest.FuzzCompile(f, pg.RunSearch, pg.SearchScope(acct, read.Scope{
		Reachable: []kernel.ULID{acct.ULID, team},
		Spaces: []accessapi.SpaceNode{
			{ID: acct.ULID, Slug: "acme", Name: "Acme"},
			{ID: team, Slug: "team", Name: "Team", Parent: acct.ULID},
		},
		Stacks: []stacksapi.Stack{{ID: kernel.NewULID(), Slug: "infra", Space: team}},
	}))
}

// TestUnitRunFields maps each of read.Fields to SQL that answers it: a
// column for every field, a cast for every order, and the space tree for
// space.
func TestUnitRunFields(t *testing.T) {
	if len(pg.RunSearch.Columns) != len(read.Fields) {
		t.Fatalf("%d columns for %d fields", len(pg.RunSearch.Columns), len(read.Fields))
	}
	for _, c := range pg.RunSearch.Columns {
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
}
