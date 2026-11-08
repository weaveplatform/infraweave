package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	kernelsearch "github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// valueLimit is how many values a suggestion lists, the most common first.
const valueLimit = 100

// Column is a searchable field and the SQL that answers it. An orderable
// Expr is never NULL, so that a keyset position always compares.
type Column struct {
	kernelsearch.Field
	// Expr is the field's SQL expression over the entity's From, text for
	// a STRING or ENUM field.
	Expr string
	// Cast is Expr's SQL type, to which a cursor's text key is cast back.
	Cast string
	// Many marks an array Expr: a value matches when an element does.
	Many bool
	// Text marks a field the full text phrase searches.
	Text bool
	// Tree is a HIERARCHY field's subquery template: the Expr values of
	// the nodes whose IDs the text[] parameter %[1]s names and, when the
	// boolean parameter %[2]s is true, of their descendants.
	Tree string
	// Nodes is a HIERARCHY field's query template: the nodes among the
	// spaces the uuid[] parameter %[1]s names, each as its Expr value as
	// text, its ID, its parent's ID or NULL, and what it shows as. It runs
	// with that parameter alone.
	Nodes string
}

// Entity is a searchable table: its columns and how a page of it reads.
// A Tree template may use the Scope's parameters.
type Entity struct {
	// List names the entity's cursors, so a cursor of another list or
	// order is refused.
	List string
	// From is the FROM clause, Select the row's columns, which a scan
	// reads first.
	From, Select string
	// Space is the row's space, which Reachable holds the row to.
	Space string
	// ID is the row's text tie-breaker, unique within From, which ends
	// every order so that a keyset position is total.
	ID      string
	Columns []Column
	// Order is the order before ID when a search names none.
	Order []kernelsearch.Order
}

// Fields are e's searchable fields, in its order.
func (e Entity) Fields() []kernelsearch.Field {
	out := make([]kernelsearch.Field, len(e.Columns))
	for i, c := range e.Columns {
		out[i] = c.Field
	}
	return out
}

func (e Entity) column(name string) Column {
	i := slices.IndexFunc(e.Columns, func(c Column) bool { return c.Name == name })
	return e.Columns[i]
}

// Scope is the rows a caller may search: Where, over From with its own
// parameters $1 to $len(Args), and the reachable spaces, which every
// compiled query ANDs as Space = ANY($n) (decision 0007 § "Evaluation").
// Reachable holds spaces of the account Where names, as an authorization
// snapshot answers them: a HIERARCHY field's Nodes lists them as they are.
type Scope struct {
	Where     string
	Args      []any
	Reachable []kernel.ULID
}

// args collects a query's parameters; add answers the next placeholder.
type args []any

func (a *args) add(v any) string {
	*a = append(*a, v)
	return "$" + strconv.Itoa(len(*a))
}

// filter is the condition of s, the predicates and the full text phrase,
// with Reachable first, its parameters added to a.
func filter(
	e Entity,
	s Scope,
	preds []kernelsearch.Predicate,
	text string,
	a *args,
) string {
	reach := s.Reachable
	if reach == nil {
		reach = []kernel.ULID{}
	}
	parts := []string{e.Space + " = ANY(" + a.add(reach) + "::uuid[])"}
	if s.Where != "" {
		parts = append(parts, "("+s.Where+")")
	}
	for _, p := range preds {
		cond := constraint(e.column(p.Field), p.Constraint, a)
		if p.Exclude {
			cond = "NOT coalesce(" + cond + ", false)"
		}
		parts = append(parts, cond)
	}
	if text != "" {
		var ors []string
		pattern := "%" + likeEscape(text) + "%"
		for _, c := range e.Columns {
			if c.Text {
				ors = append(ors, matches(c, a.add([]string{pattern})))
			}
		}
		if len(ors) > 0 {
			parts = append(parts, "("+strings.Join(ors, " OR ")+")")
		}
	}
	return strings.Join(parts, " AND ")
}

// constraint is c on column col; Check has held c to col's type.
func constraint(col Column, c kernelsearch.Constraint, a *args) string {
	switch c.Kind() {
	case "booleanEquals":
		return "(" + col.Expr + " = ANY(" + a.add(c.BooleanEquals) + "::boolean[]))"
	case "enumEquals":
		return equals(col, a.add(c.EnumEquals))
	case "stringEquals":
		return equals(col, a.add(c.StringEquals))
	case "stringMatches":
		patterns := make([]string, len(c.StringMatches))
		for i, v := range c.StringMatches {
			patterns[i] = strings.ReplaceAll(likeEscape(v), "*", "%")
		}
		return matches(col, a.add(patterns))
	case "hierarchyNodeValueEquals":
		return "(" + col.Expr + " IN (" +
			fmt.Sprintf(col.Tree, a.add(c.HierarchyValues), a.add(true)) + "))"
	case "hierarchyNode":
		deep, flat := []string{}, []string{}
		for _, n := range c.Hierarchy {
			if n.Descendants {
				deep = append(deep, n.Equals)
			} else {
				flat = append(flat, n.Equals)
			}
		}
		return "(" + col.Expr + " IN (" + fmt.Sprintf(col.Tree, a.add(deep), a.add(true)) +
			") OR " + col.Expr + " IN (" + fmt.Sprintf(col.Tree, a.add(flat), a.add(false)) + "))"
	case "timeInRange":
		parts := []string{col.Expr + " IS NOT NULL"}
		if r := c.TimeInRange; r.Start != nil {
			parts = append(parts, col.Expr+" >= to_timestamp("+a.add(*r.Start)+")")
		}
		if r := c.TimeInRange; r.End != nil {
			parts = append(parts, col.Expr+" <= to_timestamp("+a.add(*r.End)+")")
		}
		return "(" + strings.Join(parts, " AND ") + ")"
	default: // timeInLast
		d, _ := c.TimeInLast.Duration()
		return "(" + col.Expr + " >= now() - make_interval(secs => " + a.add(d.Seconds()) + "))"
	}
}

// equals is col equal to one of the text[] parameter at.
func equals(col Column, at string) string {
	if col.Many {
		return "(" + col.Expr + " && " + at + "::text[])"
	}
	return "(" + col.Expr + " = ANY(" + at + "::text[]))"
}

// matches is col matching, ignoring case, one of the LIKE patterns of the
// text[] parameter at.
func matches(col Column, at string) string {
	if col.Many {
		return "EXISTS (SELECT FROM unnest(" + col.Expr + ") v WHERE v ILIKE ANY(" + at + "::text[]))"
	}
	return "(" + col.Expr + " ILIKE ANY(" + at + "::text[]))"
}

// likeEscape makes v match itself in a LIKE pattern, under PostgreSQL's
// default escape character, the backslash.
func likeEscape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
}

// key is one column of a search's order.
type key struct {
	expr, cast string
	desc       bool
}

// statement is a compiled search: its SQL and parameters, how many key
// columns follow the entity's, the page size it reads one past, and how
// to read its rows back.
type statement struct {
	sql      string
	args     []any
	keys     int
	n        int
	backward bool
	cursor   bool
	list     string
}

// compile is in as one query over e under s. A cursor that does not
// decode, or that another list or order wrote, is a *problem.Error of type
// invalid-cursor.
func compile(e Entity, s Scope, in kernelsearch.Input) (statement, error) {
	orders := in.OrderBy
	if len(orders) == 0 {
		orders = e.Order
	}
	sig := make([]string, len(orders))
	keys := make([]key, 0, len(orders)+1)
	for i, o := range orders {
		c := e.column(o.Field)
		keys = append(keys, key{c.Expr, c.Cast, o.Desc})
		sig[i] = o.Field
		if o.Desc {
			sig[i] = "-" + o.Field
		}
	}
	keys = append(keys, key{e.ID, "text", false})
	st := statement{
		keys: len(keys), n: int(kernel.PageLimit(in.First)),
		list: e.List + "?" + strings.Join(sig, ","),
	}
	switch in.Page {
	case kernelsearch.Previous:
		st.backward, st.cursor = true, true
	case kernelsearch.Last:
		st.backward = true
	case kernelsearch.First:
	default:
		st.cursor = in.After != ""
	}
	if st.backward {
		for i := range keys {
			keys[i].desc = !keys[i].desc
		}
	}
	a := args(slices.Clone(s.Args))
	where := filter(e, s, in.Predicates, in.FullText, &a)
	if st.cursor {
		c, err := page.Decode[[]string](st.list, in.After)
		if err != nil {
			return st, err //nolint:wrapcheck // a problem, answered as it is
		}
		if len(c.Key) != len(keys)-1 {
			//nolint:wrapcheck // as above
			return st, problem.InvalidCursor.Error("The cursor's sort key does not fit this list.")
		}
		where += " AND " + after(keys, append(c.Key, c.ID), &a)
	}
	selected := make([]string, len(keys))
	order := make([]string, len(keys))
	for i, k := range keys {
		selected[i] = "(" + k.expr + ")::text"
		order[i] = k.expr + map[bool]string{false: " ASC", true: " DESC"}[k.desc]
	}
	st.sql = "SELECT " + e.Select + ", " + strings.Join(selected, ", ") +
		" FROM " + e.From + " WHERE " + where +
		" ORDER BY " + strings.Join(order, ", ") + " LIMIT " + a.add(st.n+1)
	st.args = a
	return st, nil
}

// after is the keyset condition of the rows past the position at in
// keys' order: past on the first key, or equal on it and past on the rest.
func after(keys []key, at []string, a *args) string {
	params := make([]string, len(keys))
	for i, k := range keys {
		params[i] = a.add(at[i]) + "::" + k.cast
	}
	ors := make([]string, len(keys))
	for i, k := range keys {
		var ands []string
		for j := range i {
			ands = append(ands, keys[j].expr+" = "+params[j])
		}
		ands = append(ands, k.expr+map[bool]string{false: " > ", true: " < "}[k.desc]+params[i])
		ors[i] = "(" + strings.Join(ands, " AND ") + ")"
	}
	return "(" + strings.Join(ors, " OR ") + ")"
}

// Find reads a page of e's rows under s that in keeps. scan reads a row's
// Select columns and then into extra, the key columns this package reads
// back. An input e cannot answer is a *kernelsearch.Invalid; a bad cursor
// is a *problem.Error of type invalid-cursor.
func Find[T any](
	ctx context.Context,
	q tx.Querier,
	e Entity,
	s Scope,
	in kernelsearch.Input,
	scan func(row pgx.Row, extra ...any) (T, error),
) (kernelsearch.Result[T], error) {
	var out kernelsearch.Result[T]
	if err := kernelsearch.Check(e.Fields(), in); err != nil {
		return out, err //nolint:wrapcheck // answered as it is
	}
	st, err := compile(e, s, in)
	if err != nil {
		return out, err
	}
	rows, err := q.Query(ctx, st.sql, st.args...)
	if err != nil {
		return out, fmt.Errorf("search %s: %w", e.List, sql.Unavailable(err))
	}
	defer rows.Close()
	type read struct {
		node T
		keys []string
	}
	var got []read
	for rows.Next() {
		keys := make([]string, st.keys)
		extra := make([]any, st.keys)
		for i := range keys {
			extra[i] = &keys[i]
		}
		node, err := scan(rows, extra...)
		if err != nil {
			return out, fmt.Errorf("search %s: %w", e.List, err)
		}
		got = append(got, read{node, keys})
	}
	if err := rows.Err(); err != nil {
		// A cursor's keys are cast back to their columns' types, so one a
		// client altered fails as data, not as a fault of the server.
		if pe, ok := errors.AsType[*pgconn.PgError](err); ok && st.cursor &&
			strings.HasPrefix(pe.Code, "22") {
			//nolint:wrapcheck // a problem, answered as it is
			return out, problem.InvalidCursor.Error("The cursor's sort key does not fit this list.")
		}
		return out, fmt.Errorf("search %s: %w", e.List, sql.Unavailable(err))
	}
	more := len(got) > st.n
	got = got[:min(len(got), st.n)]
	if st.backward {
		slices.Reverse(got)
	}
	for _, r := range got {
		// Encoding strings cannot fail.
		c, _ := page.Encode(st.list, kernel.Cursor[[]string]{
			Key: r.keys[:st.keys-1], ID: r.keys[st.keys-1],
		})
		out.Edges = append(out.Edges, kernelsearch.Edge[T]{Cursor: c, Node: r.node})
	}
	if len(out.Edges) > 0 {
		out.Start, out.End = out.Edges[0].Cursor, out.Edges[len(out.Edges)-1].Cursor
	}
	switch {
	case st.backward:
		out.HasPrevious, out.HasNext = more, st.cursor
	default:
		out.HasPrevious, out.HasNext = st.cursor, more
	}
	return out, nil
}

// Suggest counts e's rows under s that in keeps and lists, for each field
// in asks for, the values they hold, under the same filter, so no value or
// count reaches past the reachable spaces.
func Suggest(
	ctx context.Context,
	q tx.Querier,
	e Entity,
	s Scope,
	in kernelsearch.SuggestionsInput,
) (kernelsearch.Suggestions, error) {
	var out kernelsearch.Suggestions
	if err := kernelsearch.CheckSuggestions(e.Fields(), in); err != nil {
		return out, err //nolint:wrapcheck // answered as it is
	}
	a := args(slices.Clone(s.Args))
	where := filter(e, s, in.Predicates, in.FullText, &a)
	if err := q.QueryRow(ctx, "SELECT count(*) FROM "+e.From+" WHERE "+where, a...).
		Scan(&out.Count); err != nil {
		return out, fmt.Errorf("suggest %s: %w", e.List, sql.Unavailable(err))
	}
	for _, c := range e.Columns {
		sg := kernelsearch.Suggestion{
			Field:  c.Field,
			Listed: in.Fields == nil || slices.Contains(in.Fields, c.Name),
		}
		var err error
		switch {
		case !sg.Listed || c.Type == kernelsearch.Time:
		case c.Type == kernelsearch.Hierarchy:
			err = nodes(ctx, q, e, s, c, where, a, &sg)
		default:
			err = values(ctx, q, e, c, where, a, &sg)
		}
		if err != nil {
			return out, fmt.Errorf("suggest %s %s: %w", e.List, c.Name, sql.Unavailable(err))
		}
		out.Fields = append(out.Fields, sg)
	}
	return out, nil
}

// values lists c's most common values among the rows where keeps.
func values(
	ctx context.Context,
	q tx.Querier,
	e Entity,
	c Column,
	where string,
	a args,
	sg *kernelsearch.Suggestion,
) error {
	from, v := e.From, "("+c.Expr+")::text"
	if c.Many {
		from, v = e.From+" CROSS JOIN LATERAL unnest("+c.Expr+") AS suggested(v)", "suggested.v"
	}
	sg.Values, sg.Counts = []string{}, []int{}
	return collect(ctx, q, "SELECT "+v+", count(*) FROM "+from+" WHERE "+where+
		" AND "+v+" IS NOT NULL GROUP BY 1 ORDER BY 2 DESC, "+v+` COLLATE "C" LIMIT `+
		strconv.Itoa(valueLimit), a, func(val string, n int) {
		sg.Values = append(sg.Values, val)
		sg.Counts = append(sg.Counts, n)
	})
}

// collect runs a query of (text, count) rows and hands each to f.
func collect(ctx context.Context, q tx.Querier, query string, a args, f func(string, int)) error {
	rows, err := q.Query(ctx, query, a...)
	if err != nil {
		return err //nolint:wrapcheck // Suggest names the read
	}
	var (
		v string
		n int
	)
	_, err = pgx.ForEachRow(rows, []any{&v, &n}, func() error {
		f(v, n)
		return nil
	})
	return err //nolint:wrapcheck // as above
}

// nodes lists a HIERARCHY column's nodes in the reachable spaces, each
// counting the rows where keeps at it and below it.
func nodes(
	ctx context.Context,
	q tx.Querier,
	e Entity,
	s Scope,
	c Column,
	where string,
	a args,
	sg *kernelsearch.Suggestion,
) error {
	direct := map[string]int{}
	if err := collect(ctx, q, "SELECT ("+c.Expr+")::text, count(*) FROM "+e.From+
		" WHERE "+where+" GROUP BY 1", a, func(v string, n int) { direct[v] = n }); err != nil {
		return err
	}
	var na args
	reach := s.Reachable
	if reach == nil {
		reach = []kernel.ULID{}
	}
	rows, err := q.Query(ctx, fmt.Sprintf(c.Nodes, na.add(reach)), na...)
	if err != nil {
		return err //nolint:wrapcheck // Suggest names the read
	}
	var (
		keyOf          []string
		value, id, dis string
		parent         *string
	)
	sg.Nodes, sg.Counts, sg.Direct = []kernelsearch.HierarchyNode{}, []int{}, []int{}
	if _, err := pgx.ForEachRow(rows, []any{&value, &id, &parent, &dis}, func() error {
		keyOf = append(keyOf, value)
		sg.Nodes = append(sg.Nodes, kernelsearch.HierarchyNode{
			ID: id, Parent: deref(parent), Display: dis,
		})
		return nil
	}); err != nil {
		return err //nolint:wrapcheck // as above
	}
	at := map[string]int{}
	for i, n := range sg.Nodes {
		at[n.ID] = i
		sg.Counts = append(sg.Counts, 0)
		sg.Direct = append(sg.Direct, direct[keyOf[i]])
	}
	// Each node's rows count at it and at every ancestor listed; hops
	// bounds the walk should the parents ever form a cycle.
	for i := range sg.Nodes {
		for j, hops := i, 0; hops < len(sg.Nodes); hops++ {
			sg.Counts[j] += sg.Direct[i]
			p, ok := at[sg.Nodes[j].Parent]
			if !ok {
				break
			}
			j = p
		}
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
