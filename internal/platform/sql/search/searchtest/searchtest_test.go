package searchtest_test

import (
	"errors"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	ks "github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql/search/searchtest"
)

// things is an entity with a column of every type, its space a tree read
// from the scope.
var things = search.Entity{
	List: "things", From: "things t", Select: "t.slug", Space: "t.space_id", ID: "t.slug",
	Columns: []search.Column{
		{
			Field: ks.Field{Name: "name", Type: ks.String, Orderable: true},
			Expr:  "t.name",
			Cast:  "text",
		},
		{Field: ks.Field{Name: "label", Type: ks.String}, Expr: "t.labels", Many: true, Text: true},
		{Field: ks.Field{Name: "on", Type: ks.Boolean}, Expr: "t.on"},
		{
			Field: ks.Field{Name: "state", Type: ks.Enum, Values: []string{"UP", "DOWN"}},
			Expr:  "t.state",
		},
		{Field: ks.Field{Name: "space", Type: ks.Hierarchy}, Expr: "t.space_id", SpaceTree: true},
		{
			Field: ks.Field{Name: "createdAt", Type: ks.Time, Orderable: true},
			Expr:  "t.created_at", Cast: "timestamptz",
		},
	},
	Order: []ks.Order{{Field: "name"}},
}

// TestUnitCheck runs Check over the seeds and a fixed spread of drawn
// inputs, under a scope with a condition of its own and under one with
// none, and finds a value written into the SQL.
func TestUnitCheck(t *testing.T) {
	root := kernel.NewULID()
	scopes := map[string]search.Scope{
		"its own condition": {
			Where: "t.state <> $1", Args: []any{"GONE"}, Reachable: []kernel.ULID{root},
			Spaces: []search.SpaceNode{{Space: root, ID: "root", Display: "Root"}},
		},
		"none": {},
	}
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // a fixed spread, not a secret
	inputs := slices.Clone(searchtest.Seeds)
	for range 500 {
		b := make([]byte, r.IntN(40))
		for i := range b {
			b[i] = byte(r.UintN(256))
		}
		inputs = append(inputs, b)
	}
	for name, s := range scopes {
		for _, in := range inputs {
			if err := searchtest.Check(things, s, in); err != nil {
				t.Fatalf("%s: %q: %v", name, in, err)
			}
		}
	}

	f := &fuzzer{t: t}
	searchtest.FuzzCompile(f, things, scopes["its own condition"])
	if f.added != len(searchtest.Seeds) || f.ran != f.added {
		t.Errorf("FuzzCompile added %d seeds and ran %d", f.added, f.ran)
	}

	leaky := search.Scope{Where: "t.name <> 'iw-value'"}
	if err := searchtest.Check(things, leaky, nil); !errors.Is(err, searchtest.ErrUnsafe) {
		t.Errorf("a value in the SQL = %v", err)
	}
}

// fuzzer runs FuzzCompile's target over the seeds it adds, as a fuzz
// test's seed corpus runs.
type fuzzer struct {
	t          *testing.T
	seeds      [][]byte
	added, ran int
}

func (f *fuzzer) Helper() { f.t.Helper() }

func (f *fuzzer) Add(args ...any) {
	f.seeds = append(f.seeds, args[0].([]byte))
	f.added++
}

func (f *fuzzer) Fuzz(ff any) {
	for _, s := range f.seeds {
		ff.(func(*testing.T, []byte))(f.t, s)
		f.ran++
	}
}
