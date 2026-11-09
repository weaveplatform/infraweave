package search

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	ks "github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

const (
	tree = `SELECT c.descendant FROM closure c JOIN nodes n ON n.id = c.ancestor
		WHERE n.slug = ANY(%[1]s::text[]) AND (%[2]s::boolean OR c.depth = 0)`
	treeNodes = `SELECT n.id::text, n.slug, p.slug, n.name FROM nodes n
		LEFT JOIN nodes p ON p.id = n.parent AND p.id = ANY(%[1]s::uuid[])
		WHERE n.id = ANY(%[1]s::uuid[]) ORDER BY n.slug`
)

// items is a test entity with a column of every type, two of them full
// text, and a scope parameter of its own.
var items = Entity{
	List: "items", From: "items i", Select: "i.slug", Space: "i.space_id", ID: "i.slug",
	Columns: []Column{
		{
			Name: "name", Type: ks.String, Orderable: true,
			Expr: "i.name", Cast: "text", Text: true,
		},
		{Name: "label", Type: ks.String, Expr: "i.labels", Many: true, Text: true},
		{
			Name: "flag", Type: ks.Boolean, Orderable: true,
			Expr: "i.flag", Cast: "boolean",
		},
		{
			Name: "state", Type: ks.Enum, Values: []string{"UP", "DOWN"},
			Expr: "i.state",
		},
		{
			Name: "space", Type: ks.Hierarchy,
			Expr: "i.space_id", Tree: tree, Nodes: treeNodes,
		},
		{
			Name: "createdAt", Type: ks.Time, Orderable: true,
			Expr: "i.created_at", Cast: "timestamptz",
		},
	},
	Order: []ks.Order{{Field: "name"}},
}

var (
	reach = []kernel.ULID{kernel.NewULID(), kernel.NewULID()}
	scope = Scope{Where: "i.state <> $1", Args: []any{"GONE"}, Reachable: reach}
)

// TestUnitFilter is the golden SQL of each constraint, after the
// reachable spaces and the scope's own condition.
func TestUnitFilter(t *testing.T) {
	const head = "i.space_id = ANY($2::uuid[]) AND (i.state <> $1) AND "
	treeAt := func(nodes, deep string) string {
		return strings.NewReplacer("%[1]s", nodes, "%[2]s", deep).Replace(tree)
	}
	for _, tc := range []struct {
		name string
		pred ks.Predicate
		text string
		sql  string
		args []any
	}{
		{"stringEquals", ks.Predicate{Field: "name", Constraint: ks.Constraint{
			StringEquals: []string{"a"},
		}}, "", "(i.name = ANY($3::text[]))", []any{[]string{"a"}}},
		{"stringEquals of an array", ks.Predicate{Field: "label", Constraint: ks.Constraint{
			StringEquals: []string{"a"},
		}}, "", "(i.labels && $3::text[])", []any{[]string{"a"}}},
		{"stringMatches", ks.Predicate{Field: "name", Constraint: ks.Constraint{
			StringMatches: []string{`a*b_%\`},
		}}, "", "(i.name ILIKE ANY($3::text[]))", []any{[]string{`a%b\_\%\\`}}},
		{
			"stringMatches of an array",
			ks.Predicate{Field: "label", Constraint: ks.Constraint{
				StringMatches: []string{"x*"},
			}},
			"", "EXISTS (SELECT FROM unnest(i.labels) v WHERE v ILIKE ANY($3::text[]))",
			[]any{[]string{"x%"}},
		},
		{"booleanEquals", ks.Predicate{Field: "flag", Constraint: ks.Constraint{
			BooleanEquals: []bool{true},
		}}, "", "(i.flag = ANY($3::boolean[]))", []any{[]bool{true}}},
		{"enumEquals", ks.Predicate{Field: "state", Constraint: ks.Constraint{
			EnumEquals: []string{"UP"},
		}}, "", "(i.state = ANY($3::text[]))", []any{[]string{"UP"}}},
		{"hierarchyNodeValueEquals", ks.Predicate{Field: "space", Constraint: ks.Constraint{
			HierarchyValues: []string{"x"},
		}}, "", "(i.space_id IN (" + treeAt("$3", "$4") + "))", []any{[]string{"x"}, true}},
		{"hierarchyNode", ks.Predicate{Field: "space", Constraint: ks.Constraint{
			Hierarchy: []ks.Node{{Equals: "x", Descendants: true}, {Equals: "y"}},
		}}, "", "(i.space_id IN (" + treeAt("$3", "$4") + ") OR i.space_id IN (" +
			treeAt("$5", "$6") + "))", []any{[]string{"x"}, true, []string{"y"}, false}},
		{"timeInRange", ks.Predicate{Field: "createdAt", Constraint: ks.Constraint{
			TimeInRange: &ks.TimeRange{Start: new(int32(1)), End: new(int32(2))},
		}}, "", "(i.created_at IS NOT NULL AND i.created_at >= to_timestamp($3) AND " +
			"i.created_at <= to_timestamp($4))", []any{int32(1), int32(2)}},
		{"timeInRange, open", ks.Predicate{Field: "createdAt", Constraint: ks.Constraint{
			TimeInRange: &ks.TimeRange{},
		}}, "", "(i.created_at IS NOT NULL)", nil},
		{"timeInLast", ks.Predicate{Field: "createdAt", Constraint: ks.Constraint{
			TimeInLast: "LAST1H",
		}}, "", "(i.created_at >= now() - make_interval(secs => $3))", []any{3600.0}},
		{"exclude", ks.Predicate{Field: "name", Exclude: true, Constraint: ks.Constraint{
			StringEquals: []string{"a"},
		}}, "", "NOT coalesce((i.name = ANY($3::text[])), false)", []any{[]string{"a"}}},
		{
			"full text",
			ks.Predicate{},
			"50%",
			"((i.name ILIKE ANY($3::text[])) OR " +
				"EXISTS (SELECT FROM unnest(i.labels) v WHERE v ILIKE ANY($4::text[])))",
			[]any{[]string{`%50\%%`}, []string{`%50\%%`}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var preds []ks.Predicate
			if tc.pred.Field != "" {
				preds = []ks.Predicate{tc.pred}
			}
			a := args(scope.Args)
			got := filter(items, scope, preds, tc.text, &a)
			if want := head + tc.sql; got != want {
				t.Errorf("filter =\n%s\nwant\n%s", got, want)
			}
			if want := append(
				[]any{"GONE", reach},
				tc.args...); !reflect.DeepEqual(
				[]any(a),
				want,
			) {
				t.Errorf("args = %#v, want %#v", a, want)
			}
		})
	}
	t.Run("no full text field", func(t *testing.T) {
		var a args
		e := Entity{Space: "s", Columns: []Column{{Name: "n"}}}
		if got := filter(e, Scope{}, nil, "x", &a); got != "s = ANY($1::uuid[])" ||
			!reflect.DeepEqual(a, args{[]kernel.ULID{}}) {
			t.Errorf("filter = %s %#v", got, a)
		}
	})
}

func cursor(t *testing.T, list string, keys ...string) string {
	t.Helper()
	c, err := page.Encode(
		list,
		kernel.Cursor[[]string]{Key: keys[:len(keys)-1], ID: keys[len(keys)-1]},
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUnitCompile(t *testing.T) {
	const where = " FROM items i WHERE i.space_id = ANY($2::uuid[]) AND (i.state <> $1)"
	for _, tc := range []struct {
		name string
		in   ks.Input
		sql  string
		args []any
		st   statement
	}{
		{
			"first page in the default order",
			ks.Input{},
			"SELECT i.slug, (i.name)::text, (i.slug)::text" + where +
				" ORDER BY i.name ASC, i.slug ASC LIMIT $3",
			[]any{21},
			statement{n: 20, list: "items?name"},
		},
		{
			"next page",
			ks.Input{First: new(int32(2)), After: cursor(t, "items?name", "b", "s1")},
			"SELECT i.slug, (i.name)::text, (i.slug)::text" + where +
				" AND ((i.name > $3::text) OR (i.name = $3::text AND i.slug > $4::text))" +
				" ORDER BY i.name ASC, i.slug ASC LIMIT $5",
			[]any{"b", "s1", 3},
			statement{n: 2, list: "items?name", cursor: true},
		},
		{
			"previous page, mixed order",
			ks.Input{
				Page:    ks.Previous,
				OrderBy: []ks.Order{{Field: "createdAt", Desc: true}, {Field: "flag"}},
				After:   cursor(t, "items?-createdAt,flag", "2026-11-08 10:00:00+00", "true", "s1"),
			},
			"SELECT i.slug, (i.created_at)::text, (i.flag)::text, (i.slug)::text" + where +
				" AND ((i.created_at > $3::timestamptz)" +
				" OR (i.created_at = $3::timestamptz AND i.flag < $4::boolean)" +
				" OR (i.created_at = $3::timestamptz AND i.flag = $4::boolean AND i.slug < $5::text))" +
				" ORDER BY i.created_at ASC, i.flag DESC, i.slug DESC LIMIT $6",
			[]any{"2026-11-08 10:00:00+00", "true", "s1", 21},
			statement{n: 20, list: "items?-createdAt,flag", cursor: true, backward: true},
		},
		{
			"last page ignores the cursor",
			ks.Input{Page: ks.Last, After: "anything"},
			"SELECT i.slug, (i.name)::text, (i.slug)::text" + where +
				" ORDER BY i.name DESC, i.slug DESC LIMIT $3",
			[]any{21},
			statement{n: 20, list: "items?name", backward: true},
		},
		{
			"first page ignores the cursor",
			ks.Input{Page: ks.First, After: "anything"},
			"SELECT i.slug, (i.name)::text, (i.slug)::text" + where +
				" ORDER BY i.name ASC, i.slug ASC LIMIT $3",
			[]any{21},
			statement{n: 20, list: "items?name"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := compile(items, scope, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if st.sql != tc.sql {
				t.Errorf("sql =\n%s\nwant\n%s", st.sql, tc.sql)
			}
			if want := append([]any{"GONE", reach}, tc.args...); !reflect.DeepEqual(st.args, want) {
				t.Errorf("args = %#v, want %#v", st.args, want)
			}
			tc.st.sql, tc.st.args, tc.st.keys = st.sql, st.args, st.keys
			if !reflect.DeepEqual(st, tc.st) {
				t.Errorf("statement = %+v, want %+v", st, tc.st)
			}
		})
	}
}

func TestUnitCompileCursor(t *testing.T) {
	for name, after := range map[string]string{
		"not base64":       "%%%",
		"another order":    cursor(t, "items?-name", "b", "s1"),
		"another list":     cursor(t, "other?name", "b", "s1"),
		"keys of another":  cursor(t, "items?name", "a", "b", "s1"),
		"no key for order": cursor(t, "items?name", "s1"),
	} {
		_, err := compile(items, scope, ks.Input{After: after})
		if p, ok := errors.AsType[*problem.Error](
			err,
		); !ok ||
			p.Problem.Type != problem.InvalidCursor.New("").Type {
			t.Errorf("%s: compile = %v, want invalid-cursor", name, err)
		}
	}
}

func TestUnitFields(t *testing.T) {
	got := items.Fields()
	if len(got) != len(items.Columns) || got[3].Name != "state" || got[3].Values[1] != "DOWN" {
		t.Errorf("Fields = %+v", got)
	}
}
