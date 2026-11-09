package searchtest

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	ks "github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/sql/search"
)

// mark prefixes every value of the second input of a pair, so a value
// that reaches the SQL is found there. It holds a quote and a NUL, which
// a value written into the SQL could not hide.
const mark = "\x00'iw-value"

// Seeds are inputs for Check that draw an empty search, one with a
// cursor, values that would break out of a quoted literal, and several
// predicates of one field.
var Seeds = [][]byte{
	{},
	[]byte("\x04\x00\x01a'; DROP TABLE items; --\x02\x03\x04\x05\x06\x07\x08"),
	[]byte("\x03\x04\x01\x05x*_%\\\x01\x02\x05\x00\x03ab'\x01\x05$1::"),
	[]byte("\x02\x01\x00\x07\x02\x01\x00\x06\x03\x01\x00\x05\x04\x01\x01\x01"),
}

// Fuzzer is the part of *testing.F that FuzzCompile uses.
type Fuzzer interface {
	Helper()
	Add(args ...any)
	Fuzz(ff any)
}

// FuzzCompile runs Check over e under s from Seeds and the fuzzer's inputs.
func FuzzCompile(f Fuzzer, e search.Entity, s search.Scope) {
	f.Helper()
	for _, seed := range Seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := Check(e, s, data); err != nil {
			t.Fatal(err)
		}
	})
}

// Check draws two searches of e of one shape from data, the second with
// every value marked, and checks that they compile under s to the same
// SQL, that no value reaches it, and that it holds its rows to the
// reachable spaces as its first condition, with the reachable spaces as
// the parameter after s's own.
func Check(e search.Entity, s search.Scope, data []byte) error {
	a := (&gen{b: data, e: e, val: func(v string) string { return v }}).input()
	b := (&gen{b: data, e: e, val: func(v string) string {
		// An empty phrase searches nothing, so only a value already drawn
		// is marked, which keeps the two inputs of one shape.
		if v == "" {
			return v
		}
		return mark + v
	}}).input()
	if err := ks.Check(e.Fields(), b); err != nil {
		return fmt.Errorf("the generator drew an input the entity refuses: %w", err)
	}
	if len(data) > 0 && data[0]%2 == 0 {
		a.After, b.After = cursor(e, a.OrderBy, "k"), cursor(e, b.OrderBy, mark+"k")
	}
	var (
		sql  [2]string
		args []any
	)
	for i, in := range []ks.Input{a, b} {
		var err error
		if sql[i], args, err = search.Compile(e, s, in); err != nil {
			return fmt.Errorf("compile: %w", err)
		}
	}
	sqlA, sqlB := sql[0], sql[1]
	head := " WHERE " + e.Space + " = ANY($" + strconv.Itoa(len(s.Args)+1) + "::uuid[])"
	if s.Where != "" {
		head += " AND (" + s.Where + ")"
	}
	reach := s.Reachable
	if reach == nil {
		reach = []kernel.ULID{}
	}
	switch {
	case sqlA != sqlB:
		return fmt.Errorf("%w: the values changed the SQL:\n%s\n%s", ErrUnsafe, sqlA, sqlB)
	case strings.Contains(sqlB, "iw-value"):
		return fmt.Errorf("%w: a value reached the SQL: %s", ErrUnsafe, sqlB)
	case !strings.Contains(sqlB, head+" AND ") && !strings.Contains(sqlB, head+" ORDER BY "):
		return fmt.Errorf("%w: the SQL does not hold its rows to the reachable spaces: %s",
			ErrUnsafe, sqlB)
	case !reflect.DeepEqual(args[len(s.Args)], reach):
		return fmt.Errorf("%w: $%d = %#v, want the reachable spaces", ErrUnsafe,
			len(s.Args)+1, args[len(s.Args)])
	}
	return nil
}

// ErrUnsafe is a compiled search that breaks a rule Check holds it to.
var ErrUnsafe = errors.New("unsafe search")

// cursor is a cursor of e's list in orders, every key value v.
func cursor(e search.Entity, orders []ks.Order, v string) string {
	if len(orders) == 0 {
		orders = e.Order
	}
	sig := make([]string, len(orders))
	keys := make([]string, len(orders))
	for i, o := range orders {
		sig[i] = o.Field
		if o.Desc {
			sig[i] = "-" + o.Field
		}
		keys[i] = v
	}
	// A key of strings always encodes as JSON, so Encode cannot fail here.
	c, _ := page.Encode(
		e.List+"?"+strings.Join(sig, ","),
		kernel.Cursor[[]string]{Key: keys, ID: v},
	)
	return c
}

// gen draws a search of e from b, passing every value it draws through val.
type gen struct {
	b   []byte
	e   search.Entity
	val func(string) string
}

func (g *gen) pick(n int) int {
	if len(g.b) == 0 || n == 0 {
		return 0
	}
	v := int(g.b[0]) % n
	g.b = g.b[1:]
	return v
}

func (g *gen) str() string {
	n := min(g.pick(12), len(g.b))
	v := string(g.b[:n])
	g.b = g.b[n:]
	return g.val(v)
}

func (g *gen) strs() []string {
	out := make([]string, g.pick(4))
	for i := range out {
		out[i] = g.str()
	}
	return out
}

func (g *gen) input() ks.Input {
	var in ks.Input
	for range g.pick(5) {
		c := g.e.Columns[g.pick(len(g.e.Columns))]
		in.Predicates = append(in.Predicates, ks.Predicate{
			Field: c.Name, Exclude: g.pick(2) == 0, Constraint: g.constraint(c.Field),
		})
	}
	in.FullText = g.str()
	var orderable []string
	for _, c := range g.e.Columns {
		if c.Orderable {
			orderable = append(orderable, c.Name)
		}
	}
	if len(orderable) > 0 && g.pick(2) == 0 {
		in.OrderBy = []ks.Order{{Field: orderable[g.pick(len(orderable))], Desc: g.pick(2) == 0}}
	}
	return in
}

// constraint draws a constraint that applies to f's type.
func (g *gen) constraint(f ks.Field) ks.Constraint {
	var k ks.Constraint
	switch f.Type {
	case ks.String:
		if g.pick(2) == 0 {
			k.StringEquals = g.strs()
		} else {
			k.StringMatches = g.strs()
		}
	case ks.Boolean:
		k.BooleanEquals = []bool{g.pick(2) == 0}
	case ks.Enum:
		k.EnumEquals = []string{f.Values[g.pick(len(f.Values))]}
	case ks.Hierarchy:
		if g.pick(2) == 0 {
			k.HierarchyValues = g.strs()
			break
		}
		k.Hierarchy = []ks.Node{}
		for range g.pick(3) {
			k.Hierarchy = append(k.Hierarchy, ks.Node{Equals: g.str(), Descendants: g.pick(2) == 0})
		}
	default:
		if g.pick(2) == 0 {
			start := int32(g.pick(1000)) //nolint:gosec // below 1000
			k.TimeInRange = &ks.TimeRange{Start: &start}
		} else {
			k.TimeInLast = "LAST7D"
		}
	}
	return k
}
