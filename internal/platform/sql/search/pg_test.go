package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	ks "github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m)) }

// world is a tree of five nodes, root above a and b, a above a1 and a2,
// with two items in each node and a third, GONE, that the scope hides.
type world struct {
	db    *pgxpool.Pool
	nodes map[string]kernel.ULID
}

func newWorld(t *testing.T) world {
	t.Helper()
	ctx := t.Context()
	w := world{db: sqltest.Open(t).Primary(), nodes: map[string]kernel.ULID{}}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(
		`CREATE TABLE nodes (id uuid PRIMARY KEY, slug text NOT NULL, parent uuid, name text NOT NULL)`,
	)
	exec(`CREATE TABLE closure (ancestor uuid, descendant uuid, depth int)`)
	exec(`CREATE TABLE items (slug text PRIMARY KEY, space_id uuid NOT NULL, name text NOT NULL,
		labels text[] NOT NULL, flag boolean NOT NULL, state text NOT NULL,
		created_at timestamptz NOT NULL)`)
	parents := []struct{ slug, parent string }{
		{"root", ""}, {"a", "root"}, {"b", "root"}, {"a1", "a"}, {"a2", "a"},
	}
	for i, n := range parents {
		id := kernel.NewULID()
		w.nodes[n.slug] = id
		var parent *kernel.ULID
		if n.parent != "" {
			p := w.nodes[n.parent]
			parent = &p
		}
		exec(
			`INSERT INTO nodes VALUES ($1, $2, $3, $4)`,
			id,
			n.slug,
			parent,
			strings.ToUpper(n.slug),
		)
		exec(
			`INSERT INTO closure SELECT ancestor, $1::uuid, depth + 1 FROM closure WHERE descendant = $2
			UNION ALL SELECT $1::uuid, $1::uuid, 0`,
			id,
			parent,
		)
		for j, state := range []string{"UP", "DOWN", "GONE"} {
			exec(`INSERT INTO items VALUES ($1, $2, $3, $4, $5, $6,
					'2026-11-01'::timestamptz + make_interval(hours => $7))`,
				fmt.Sprintf("%s-%d", n.slug, j), id, fmt.Sprintf("Item %d of %s", j, n.slug),
				[]string{"node-" + n.slug, state}, j == 0, state, i*3+j)
		}
	}
	return w
}

func (w world) reach(slugs ...string) []kernel.ULID {
	out := make([]kernel.ULID, len(slugs))
	for i, s := range slugs {
		out[i] = w.nodes[s]
	}
	return out
}

func scanSlug(row pgx.Row, extra ...any) (string, error) {
	var s string
	return s, row.Scan(append([]any{&s}, extra...)...)
}

func (w world) find(t *testing.T, s Scope, in ks.Input) ks.Result[string] {
	t.Helper()
	r, err := Find(t.Context(), w.db, items, s, in, scanSlug)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func slugs(r ks.Result[string]) []string {
	out := make([]string, 0, len(r.Edges))
	for _, e := range r.Edges {
		out = append(out, e.Node)
	}
	return out
}

// TestAccFindPaging pages through the reachable rows forwards and back:
// every page is full but the last, no row repeats or goes missing, and a
// row of an unreachable space or outside the scope never appears.
func TestAccFindPaging(t *testing.T) {
	w := newWorld(t)
	s := Scope{Where: "i.state <> $1", Args: []any{"GONE"}, Reachable: w.reach("a", "a1", "b")}
	want := []string{"a-0", "a-1", "b-0", "b-1", "a1-0", "a1-1"}
	var got []string
	var pages []ks.Result[string]
	in := ks.Input{First: new(int32(4)), OrderBy: []ks.Order{{Field: "createdAt"}}}
	for {
		r := w.find(t, s, in)
		pages = append(pages, r)
		got = append(got, slugs(r)...)
		if !r.HasNext {
			break
		}
		if len(r.Edges) != 4 {
			t.Fatalf("a page before the last holds %d rows", len(r.Edges))
		}
		in.After = r.End
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paged %v, want %v", got, want)
	}
	if len(pages) != 2 || pages[0].HasPrevious || !pages[1].HasPrevious {
		t.Fatalf("pages = %+v", pages)
	}
	back := w.find(t, s, ks.Input{
		First: new(int32(4)), OrderBy: in.OrderBy, Page: ks.Previous, After: pages[1].Start,
	})
	if b := slugs(back); !reflect.DeepEqual(b, want[:4]) || back.HasPrevious || !back.HasNext {
		t.Errorf("previous page = %v %+v, want %v", b, back, want[:4])
	}
	last := w.find(t, s, ks.Input{First: new(int32(4)), OrderBy: in.OrderBy, Page: ks.Last})
	if l := slugs(last); !reflect.DeepEqual(l, want[2:]) || !last.HasPrevious || last.HasNext {
		t.Errorf("last page = %v %+v, want %v", l, last, want[2:])
	}
	desc := w.find(t, s, ks.Input{
		First:   new(int32(2)),
		OrderBy: []ks.Order{{Field: "flag", Desc: true}, {Field: "name"}},
	})
	if d := slugs(desc); !reflect.DeepEqual(d, []string{"a-0", "a1-0"}) {
		t.Errorf("by flag, then name = %v", d)
	}
	next := w.find(t, s, ks.Input{First: new(int32(2)), OrderBy: []ks.Order{
		{Field: "flag", Desc: true},
		{Field: "name"},
	}, After: desc.End})
	if d := slugs(next); !reflect.DeepEqual(d, []string{"b-0", "a-1"}) {
		t.Errorf("by flag, then name, next = %v", d)
	}
	if none := w.find(t, Scope{}, ks.Input{}); len(none.Edges) != 0 || none.Start != "" {
		t.Errorf("no reachable space found %v", slugs(none))
	}
}

// TestAccFindConstraints runs a predicate of each constraint against the
// rows of a and its descendants.
func TestAccFindConstraints(t *testing.T) {
	w := newWorld(t)
	s := Scope{Reachable: w.reach("root", "a", "b", "a1", "a2")}
	for _, tc := range []struct {
		name string
		in   ks.Input
		want string
	}{
		{"stringEquals", ks.Input{Predicates: []ks.Predicate{{
			Field:      "name",
			Constraint: ks.Constraint{StringEquals: []string{"Item 1 of a"}},
		}}}, "a-1"},
		{"stringMatches ignores case", ks.Input{Predicates: []ks.Predicate{{
			Field:      "name",
			Constraint: ks.Constraint{StringMatches: []string{"*0 OF A*"}},
		}}}, "a-0,a1-0,a2-0"},
		{"stringMatches is literal but for *", ks.Input{Predicates: []ks.Predicate{{
			Field:      "name",
			Constraint: ks.Constraint{StringMatches: []string{"Item _ of a"}},
		}}}, ""},
		{"label", ks.Input{Predicates: []ks.Predicate{
			{Field: "label", Constraint: ks.Constraint{StringEquals: []string{"node-b"}}},
			{Field: "label", Exclude: true, Constraint: ks.Constraint{StringMatches: []string{"g*"}}},
		}}, "b-0,b-1"},
		{"booleanEquals", ks.Input{Predicates: []ks.Predicate{
			{Field: "flag", Constraint: ks.Constraint{BooleanEquals: []bool{true}}},
			{Field: "space", Constraint: ks.Constraint{HierarchyValues: []string{"a"}}},
		}}, "a-0,a1-0,a2-0"},
		{"enumEquals", ks.Input{Predicates: []ks.Predicate{
			{Field: "state", Constraint: ks.Constraint{EnumEquals: []string{"DOWN"}}},
			{Field: "space", Constraint: ks.Constraint{Hierarchy: []ks.Node{{Equals: "a"}, {Equals: "a2"}}}},
		}}, "a-1,a2-1"},
		{"hierarchyNode", ks.Input{Predicates: []ks.Predicate{
			{Field: "space", Constraint: ks.Constraint{
				Hierarchy: []ks.Node{{Equals: "a", Descendants: true}, {Equals: "b"}},
			}},
			{Field: "state", Constraint: ks.Constraint{EnumEquals: []string{"UP"}}},
		}}, "a-0,a1-0,a2-0,b-0"},
		{"timeInRange", ks.Input{Predicates: []ks.Predicate{{
			Field: "createdAt",
			Constraint: ks.Constraint{TimeInRange: &ks.TimeRange{
				Start: new(int32(1793491200 + 3*3600)), End: new(int32(1793491200 + 4*3600)),
			}},
		}}}, "a-0,a-1"},
		{"timeInLast", ks.Input{Predicates: []ks.Predicate{{
			Field:      "createdAt",
			Constraint: ks.Constraint{TimeInLast: "LAST15M"},
		}}}, ""},
		{"full text", ks.Input{FullText: "NODE-A2"}, "a2-0,a2-1,a2-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(slugs(w.find(t, s, tc.in)), ","); got != tc.want {
				t.Errorf("found %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAccSuggest checks that values and counts come only from the rows
// the scope and the reachable spaces keep.
func TestAccSuggest(t *testing.T) {
	w := newWorld(t)
	s := Scope{Where: "i.state <> $1", Args: []any{"GONE"}, Reachable: w.reach("a", "a1", "b")}
	got, err := Suggest(t.Context(), w.db, items, s, ks.SuggestionsInput{
		Fields: []string{"label", "flag", "space", "createdAt", "nope"},
		Predicates: []ks.Predicate{{
			Field: "name", Exclude: true,
			Constraint: ks.Constraint{StringEquals: []string{"Item 0 of b"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 5 {
		t.Errorf("count = %d, want 5", got.Count)
	}
	by := map[string]ks.Suggestion{}
	for _, f := range got.Fields {
		by[f.Field.Name] = f
	}
	if f := by["name"]; f.Listed || f.Values != nil {
		t.Errorf("name was not asked for: %+v", f)
	}
	if f := by["label"]; !reflect.DeepEqual(
		f.Values,
		[]string{"DOWN", "UP", "node-a", "node-a1", "node-b"},
	) ||
		!reflect.DeepEqual(f.Counts, []int{3, 2, 2, 2, 1}) {
		t.Errorf("label = %v %v", f.Values, f.Counts)
	}
	if f := by["flag"]; !reflect.DeepEqual(f.Values, []string{"false", "true"}) ||
		!reflect.DeepEqual(f.Counts, []int{3, 2}) {
		t.Errorf("flag = %v %v", f.Values, f.Counts)
	}
	if f := by["createdAt"]; !f.Listed || f.Values != nil {
		t.Errorf("createdAt = %+v, want listed with no values", f)
	}
	f := by["space"]
	nodes := []ks.HierarchyNode{
		{ID: "a", Display: "A"},
		{ID: "a1", Parent: "a", Display: "A1"},
		{ID: "b", Display: "B"},
	}
	if !reflect.DeepEqual(f.Nodes, nodes) || !reflect.DeepEqual(f.Counts, []int{4, 2, 1}) ||
		!reflect.DeepEqual(f.Direct, []int{2, 2, 1}) {
		t.Errorf("space = %+v", f)
	}
	_, err = Suggest(t.Context(), w.db, items, s, ks.SuggestionsInput{
		Predicates: []ks.Predicate{{Field: "nope"}},
	})
	if _, ok := errors.AsType[*ks.Invalid](err); !ok {
		t.Errorf("Suggest of an unknown field = %v, want *Invalid", err)
	}
}

func TestAccFindRefused(t *testing.T) {
	w := newWorld(t)
	_, err := Find(
		t.Context(),
		w.db,
		items,
		Scope{},
		ks.Input{OrderBy: []ks.Order{{Field: "label"}}},
		scanSlug,
	)
	if _, ok := errors.AsType[*ks.Invalid](err); !ok {
		t.Errorf("Find ordered by label = %v, want *Invalid", err)
	}
	if _, err := Find(
		t.Context(),
		w.db,
		items,
		Scope{},
		ks.Input{After: "x"},
		scanSlug,
	); err == nil {
		t.Error("Find after a bad cursor succeeded")
	}
	failing := errors.New("scan failed")
	_, err = Find(t.Context(), w.db, items, Scope{Reachable: w.reach("a")}, ks.Input{},
		func(pgx.Row, ...any) (string, error) { return "", failing })
	if !errors.Is(err, failing) {
		t.Errorf("Find with a failing scan = %v", err)
	}
}

// broken is a querier whose every query fails.
type broken struct{ tx.Querier }

var errBroken = errors.New("broken")

func (broken) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, errBroken }

func (broken) QueryRow(context.Context, string, ...any) pgx.Row { return brokenRow{} }

type brokenRow struct{}

func (brokenRow) Scan(...any) error { return errBroken }

// flaky passes the first ok queries to its pool and fails the rest.
type flaky struct {
	*pgxpool.Pool
	ok *int
}

func (f flaky) Query(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
	if *f.ok == 0 {
		return nil, errBroken
	}
	*f.ok--
	return f.Pool.Query(ctx, q, args...)
}

// TestAccSuggestFailures fails each of a suggestion's reads in turn, and
// reads one whose nodes do not scan.
func TestAccSuggestFailures(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	for ok := range 3 {
		q := flaky{w.db, &ok}
		in := ks.SuggestionsInput{Fields: []string{"label", "space"}}
		if _, err := Suggest(ctx, q, items, Scope{}, in); !errors.Is(err, errBroken) {
			t.Errorf("Suggest failing after %d reads = %v", ok, err)
		}
	}
	bad := items
	bad.Columns = slices.Clone(items.Columns)
	bad.Columns[4].Nodes = `SELECT 1 FROM nodes WHERE id = ANY(%[1]s::uuid[])`
	if _, err := Suggest(ctx, w.db, bad, Scope{Reachable: w.reach("a")},
		ks.SuggestionsInput{Fields: []string{"space"}}); err == nil {
		t.Error("Suggest of nodes that do not scan succeeded")
	}
	got, err := Suggest(ctx, w.db, items, Scope{}, ks.SuggestionsInput{Fields: []string{"space"}})
	if err != nil || got.Count != 0 || len(got.Fields[4].Nodes) != 0 {
		t.Errorf("Suggest with no reachable space = %+v, %v", got, err)
	}
}

// TestAccFindAlteredCursor reads after a cursor whose time key is not a
// time, which answers as an invalid cursor.
func TestAccFindAlteredCursor(t *testing.T) {
	w := newWorld(t)
	_, err := Find(t.Context(), w.db, items, Scope{Reachable: w.reach("a")}, ks.Input{
		OrderBy: []ks.Order{{Field: "createdAt"}},
		After:   cursor(t, "items?createdAt", "yesterday-ish", "a-0"),
	}, scanSlug)
	if p, ok := errors.AsType[*problem.Error](err); !ok || p.Problem.Status != 400 {
		t.Errorf("Find after an altered cursor = %v, want invalid-cursor", err)
	}
	_, err = Find(t.Context(), flaky{w.db, new(0)}, items, Scope{}, ks.Input{}, scanSlug)
	if !errors.Is(err, errBroken) {
		t.Errorf("Find = %v", err)
	}
}

func TestUnitQueryFailures(t *testing.T) {
	ctx := t.Context()
	if _, err := Find(
		ctx,
		broken{},
		items,
		Scope{},
		ks.Input{},
		scanSlug,
	); !errors.Is(
		err,
		errBroken,
	) {
		t.Errorf("Find = %v", err)
	}
	if _, err := Suggest(
		ctx,
		broken{},
		items,
		Scope{},
		ks.SuggestionsInput{},
	); !errors.Is(
		err,
		errBroken,
	) {
		t.Errorf("Suggest = %v", err)
	}
}
