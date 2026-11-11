package expr_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
)

// inputsSchema declares the test surface's inputs: an object type with a
// name, a count, string tags and a list of objects.
const inputsSchema = `{
	"type": "object",
	"properties": {
		"name": {"type": "string"},
		"count": {"type": "integer"},
		"ratio": {"type": "number"},
		"tags": {"type": "object", "additionalProperties": {"type": "string"}},
		"subnets": {"type": "array", "items": {
			"type": "object",
			"properties": {"cidr": {"type": "string"}}
		}}
	}
}`

// env describes every context as an empty one.
func env(context.Context, kernel.Principal, string) (expr.Context, error) {
	return expr.Context{}, nil
}

// surface is the test surface: inputs, free-form data and secrets.
func surface() expr.Surface {
	return expr.Surface{
		Name: "test",
		Variables: []expr.Variable{
			{Name: "inputs", Schema: json.RawMessage(inputsSchema)},
			{Name: "data"},
			{Name: "secrets", Secret: true},
		},
	}
}

func registry(t *testing.T, cfg expr.Config, surfaces ...expr.Surface) *expr.Registry {
	t.Helper()
	if len(surfaces) == 0 {
		surfaces = []expr.Surface{surface()}
	}
	r, err := expr.NewRegistry(cfg, surfaces...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// input is a fixed evaluation context. Its numbers are float64, as JSON
// decodes them.
func input() expr.Input {
	return expr.Input{
		Now:  time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC),
		Seed: 42,
		Values: map[string]any{
			"inputs": map[string]any{
				"name":    "web",
				"count":   3.0,
				"ratio":   2.0,
				"tags":    map[string]any{"team": "core", "env": "prod", "app": "web"},
				"subnets": []any{map[string]any{"cidr": "10.0.0.0/24"}},
			},
			"data": map[string]any{"z": 1.0, "y": 2.0, "x": []any{true, nil}},
		},
		Secrets: func(_ context.Context, _ string, keys []string) (map[string]string, error) {
			all := map[string]string{"TOKEN": "tok-5ecret-value", "OTHER": "other-secret"}
			out := map[string]string{}
			for _, k := range keys {
				if v, ok := all[k]; ok {
					out[k] = v
				}
			}
			return out, nil
		},
	}
}

func eval(t *testing.T, r *expr.Registry, form expr.Form, src string) (expr.Result, error) {
	t.Helper()
	return r.Evaluate(t.Context(), expr.Request{Surface: "test", Form: form, Source: src}, input())
}

// TestUnitExprLibrary evaluates every function of the library, cel-go's
// and infraweave's, and their errors, and holds the completion list to
// the environment: every function the decision names is listed, with a
// signature, and nothing internal leaks into it.
func TestUnitExprLibrary(t *testing.T) {
	r := registry(t, expr.Defaults())
	for _, c := range []struct {
		src  string
		want any
	}{
		// Core, macros and optional values.
		{`inputs.subnets.all(s, s.cidr.startsWith("10."))`, true},
		{`[1, 2, 3].exists_one(x, x > 2)`, true},
		{`[1, 2, 3].filter(x, x > 1).map(x, x * 10)`, []any{int64(20), int64(30)}},
		{`has(inputs.tags.team) && !has(inputs.tags.owner)`, true},
		{`inputs.tags[?"owner"].orValue("nobody")`, "nobody"},
		{`inputs.?tags.?team.hasValue()`, true},
		{`size(inputs.name) + inputs.count`, int64(6)},
		{`inputs.ratio * 2.0`, 4.0},
		{`1 < 1.5 && 2u > 1`, true},
		// Strings.
		{`"a,b,c".split(",").join("-")`, "a-b-c"},
		{`" Web ".trim().lowerAscii().upperAscii().charAt(0)`, "W"},
		{`"abcabc".indexOf("c") + "abcabc".lastIndexOf("c")`, int64(7)},
		{`"hello".replace("l", "L").substring(1, 4).reverse()`, "LLe"},
		{`"%s=%d".format(["n", 3])`, "n=3"},
		{`strings.quote("a\"b")`, `"a\"b"`},
		// Lists.
		{`[3, 1, 2].sort()`, []any{int64(1), int64(2), int64(3)}},
		{`[[1], [2, 3]].flatten().distinct().slice(1, 3)`, []any{int64(2), int64(3)}},
		{`["bb", "a"].sortBy(s, size(s)).first().value()`, "a"},
		{`[1, 2].last().value() + lists.range(3).size()`, int64(5)},
		{`[1, 2].transformList(i, v, v * i)`, []any{int64(0), int64(2)}},
		{`{"a": 1}.transformMap(k, v, v + 1)`, map[string]any{"a": int64(2)}},
		{`{"a": 1}.transformMapEntry(k, v, {k + k: v})`, map[string]any{"aa": int64(1)}},
		// Maps.
		{
			`merge(inputs.tags, {"env": "dev", "x": "1"})`,
			map[string]any{"team": "core", "env": "dev", "app": "web", "x": "1"},
		},
		{`pick(inputs.tags, ["env", "missing"])`, map[string]any{"env": "prod"}},
		{`omit(inputs.tags, ["env", "app"])`, map[string]any{"team": "core"}},
		// Regular expressions.
		{`inputs.name.matches("^w") && matches("ab", "b$")`, true},
		{`regex.extract("ab12cd34", "[0-9]+").value()`, "12"},
		{`regex.extractAll("ab12cd34", "[0-9]+")`, []any{"12", "34"}},
		{`regex.replace("ab12", "[0-9]", "#")`, "ab##"},
		// Encoding.
		{`base64.encode(b"hello")`, "aGVsbG8="},
		{`base64.decode("aGVsbG8=") == base64.decode("aGVsbG8")`, true},
		{`url.encode("a b&c/d")`, "a+b%26c%2Fd"},
		{`url.decode("a+b%26c")`, "a b&c"},
		{`url.query({"q": "a b", "tag": ["x", "y"], "n": 1, "on": true})`, "n=1&on=true&q=a+b&tag=x&tag=y"},
		{
			`json.encode({"b": [1, 2.5, "x", null, true], "a": {"z": b"hi"}})`,
			`{"a":{"z":"aGk="},"b":[1,2.5,"x",null,true]}`,
		},
		{
			`json.encode([now, duration("90s"), optional.none(), optional.of(1u), {1: "int key"}])`,
			`["2026-11-09T08:30:00Z","90s",null,1,{"1":"int key"}]`,
		},
		{`json.decode('{"a": [1, "x"]}').a`, []any{1.0, "x"}},
		// Maths.
		{`math.greatest(1, 5, 3) + math.least([4, 2])`, int64(7)},
		{`math.ceil(1.2) + math.floor(1.8) + math.round(1.5) + math.abs(-1.0) + math.sign(-3.0)`, 5.0},
		// Time and durations.
		{`now + duration("1h") > now && now.getHours() == 8`, true},
		{`now.getHours("Europe/London")`, int64(8)},
		{`timestamp("2026-01-01T00:00:00Z").getFullYear()`, int64(2026)},
		{`duration("90m").getHours()`, int64(1)},
		// Locals.
		{`cel.bind(n, inputs.count, n * n)`, int64(9)},
		// Free-form data.
		{`data.z + data.y`, 3.0},
		{`data.x[1] == null`, true},
		{`type(data) == map`, true},
		{`type(1)`, "int"},
	} {
		got, err := eval(t, r, expr.FormExpression, c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if !reflect.DeepEqual(got.Value, c.want) {
			t.Errorf("%s = %#v, want %#v", c.src, got.Value, c.want)
		}
	}

	for _, c := range []struct{ src, want string }{
		{`merge(1, {})`, "expected a map, got int"},
		{`merge({}, "x")`, "expected a map, got string"},
		{`pick([1], ["a"])`, "expected a map, got list"},
		{`url.decode("%zz")`, "url.decode"},
		{`json.decode("{")`, "json.decode"},
		{`json.encode(0.0 / 0.0)`, "json.encode: NaN has no JSON form"},
		{`base64.decode("!!")`, "base64.decode"},
		{`random.int(2, 1)`, "min 2 is greater than max 1"},
		{`random.string(-1)`, "random.string: length -1 is negative"},
		{`fail("no " + inputs.name)`, "no web"},
		{`inputs.tags.owner`, "The evaluation failed: no such key: owner."},
	} {
		_, err := eval(t, r, expr.FormExpression, c.src)
		if f, ok := errors.AsType[*expr.Failed](err); !ok || !strings.Contains(f.Message, c.want) {
			t.Errorf("%s: %v, want a failure with %q", c.src, err, c.want)
		}
	}

	listed := map[string]expr.Function{}
	for _, f := range r.Functions() {
		if len(f.Signatures) == 0 || f.Signatures[0] == "" {
			t.Errorf("%s has no signature", f.Name)
		}
		if strings.ContainsAny(f.Name, "@") || strings.HasPrefix(f.Name, "_") {
			t.Errorf("an internal function is listed: %s", f.Name)
		}
		if _, dup := listed[f.Name]; !dup || !f.Macro {
			listed[f.Name] = f
		}
	}
	for _, name := range []string{
		// infraweave's, each with its description.
		"base64.encode", "base64.decode", "url.encode", "url.decode", "url.query",
		"json.encode", "json.decode", "merge", "pick", "omit",
		"random.uuid", "random.int", "random.string", "fail",
		// cel-go's, each one the library's table names.
		"all", "exists", "exists_one", "map", "filter", "has", "hasValue", "orValue",
		"charAt", "indexOf", "lastIndexOf", "lowerAscii", "upperAscii", "replace",
		"split", "join", "substring", "trim", "format", "strings.quote", "reverse",
		"slice", "flatten", "distinct", "sort", "sortBy", "first", "last", "lists.range",
		"transformList", "transformMap", "transformMapEntry", "matches",
		"regex.extract", "regex.extractAll", "regex.replace",
		"math.greatest", "math.least", "math.ceil", "math.floor", "math.round",
		"math.abs", "math.sign", "timestamp", "duration", "getHours", "cel.bind",
	} {
		if _, ok := listed[name]; !ok {
			t.Errorf("%s is not in the function list", name)
		}
	}
	for _, name := range []string{"merge", "json.encode", "random.uuid", "fail"} {
		if listed[name].Description == "" {
			t.Errorf("%s has no description", name)
		}
	}
	if got := listed["random.int"].Signatures; !slices.Equal(
		got,
		[]string{"random.int(int, int) -> int"},
	) {
		t.Errorf("random.int's signatures %v", got)
	}
	if got := listed["all"]; !got.Macro || !slices.Contains(got.Signatures, "_.all(_, _)") {
		t.Errorf("the all macro %+v", got)
	}
	if got := listed["math.greatest"]; !got.Macro ||
		!slices.Equal(got.Signatures, []string{"math.greatest(…)"}) {
		t.Errorf("a namespaced variadic macro %+v", got)
	}
	if got := listed["cel.bind"]; !slices.Equal(got.Signatures, []string{"cel.bind(_, _, _)"}) {
		t.Errorf("cel.bind %+v", got)
	}
	if got := listed["has"]; !slices.Equal(got.Signatures, []string{"has(_)"}) {
		t.Errorf("has %+v", got)
	}
}

// TestUnitExprTemplates renders every CEL type into text, the $${{
// escape, and string literals and braces inside ${{ … }}; a template that
// is one whole value keeps its type.
func TestUnitExprTemplates(t *testing.T) {
	r := registry(t, expr.Defaults())
	for _, c := range []struct{ src, want string }{
		{"plain text", "plain text"},
		{"", ""},
		{"name=${{ inputs.name }}", "name=web"},
		{"${{ 3 }} ${{ 3u }} ${{ 2.5 }} ${{ true }}", "3 3 2.5 true"},
		{"${{ duration('90m') }}|${{ now }}", "5400s|2026-11-09T08:30:00Z"},
		{"[${{ null }}][${{ optional.none() }}][${{ optional.of('x') }}]", "[][][x]"},
		{"${{ b'hi' }} ${{ type(1) }}", "aGk= int"},
		{"tags ${{ inputs.tags }}", `tags {"app":"web","env":"prod","team":"core"}`},
		{"list ${{ [1, 'a', {'b': [2.0]}] }}", `list [1,"a",{"b":[2]}]`},
		{"a $${{ b }} c", "a ${{ b }} c"},
		{"$$${{ 1 }}", "$${{ 1 }}"},
		{"${{ {'x': '}}'}.x }}", "}}"},
		{`${{ "}}" + '}}' + """}}""" + '''}}''' + r"\}}" }}`, `}}}}}}}}\}}`},
		{`${{ "a\"}}" }}`, `a"}}`},
		{"${{ {'a': {'b': 1}}.a.b }}!", "1!"},
		{"${{ 'é' }}é", "éé"},
		{"line1\n${{ inputs.count }}\n", "line1\n3\n"},
	} {
		got, err := eval(t, r, expr.FormTemplate, c.src)
		if err != nil {
			t.Errorf("%q: %v", c.src, err)
			continue
		}
		if got.Value != c.want || got.Type != "string" {
			t.Errorf("%q = %#v (%s), want %q", c.src, got.Value, got.Type, c.want)
		}
	}

	for _, c := range []struct {
		src  string
		want any
		typ  string
	}{
		{"${{ inputs.count }}", int64(3), "int"},
		{"${{inputs.tags}}", map[string]any{"team": "core", "env": "prod", "app": "web"}, "map(string, string)"},
		{"${{ [1, 2] }}", []any{int64(1), int64(2)}, "list(int)"},
		{"${{ null }}", nil, "null"},
	} {
		got, err := eval(t, r, expr.FormTemplate, c.src)
		if err != nil || !reflect.DeepEqual(got.Value, c.want) || got.Type != c.typ {
			t.Errorf(
				"whole value %q = %#v (%s), %v; want %#v (%s)",
				c.src,
				got.Value,
				got.Type,
				err,
				c.want,
				c.typ,
			)
		}
	}

	for _, c := range []struct {
		src  string
		want kernel.Range
		msg  string
	}{
		{"ok ${{ 1 +", rng(1, 4, 1, 7), "never closed"},
		{"x\n  ${{ 'unclosed }}", rng(2, 3, 2, 6), "never closed"},
		{"x\n  ${{ inputs.nmae }}", rng(2, 14, 2, 18), "undefined field 'nmae'"},
		{"${{ 1 }} ${{ ) }}", rng(1, 14, 1, 15), "Syntax error"},
	} {
		chk, err := r.Check(expr.Request{Surface: "test", Form: expr.FormTemplate, Source: c.src})
		if err != nil || chk.OK() || len(chk.Diagnostics) != 1 ||
			chk.Diagnostics[0].Range != c.want || !strings.Contains(chk.Diagnostics[0].Message, c.msg) ||
			chk.Type != "" {
			t.Errorf("%q: %+v, %v; want %q at %+v", c.src, chk, err, c.msg, c.want)
		}
	}

	small := expr.Defaults()
	small.MaxResult = 8
	r = registry(t, small)
	for _, src := range []string{
		"${{ 'abcde' }}${{ 'fghij' }}", "${{ 'a' }}0123456789", "${{ 'abcdefghij' }}",
		"${{ random.string(9) }}",
	} {
		_, err := eval(t, r, expr.FormTemplate, src)
		if f, ok := errors.AsType[*expr.Failed](err); !ok || f.Limit != "expressions.max_result" {
			t.Errorf("%q over max_result: %v", src, err)
		}
	}
}

func rng(l1, c1, l2, c2 int) kernel.Range {
	return kernel.Range{
		Start: kernel.Position{Line: l1, Column: c1},
		End:   kernel.Position{Line: l2, Column: c2},
	}
}

// TestUnitExprDeterminism evaluates now, every random.* function and map
// comprehensions twice with the same Input and requires identical
// results; another seed draws other values, and every map, given or
// built, is iterated in key order.
func TestUnitExprDeterminism(t *testing.T) {
	r := registry(t, expr.Defaults())
	const src = `{
		"now": now,
		"uuid": random.uuid(),
		"uuids": [random.uuid(), random.uuid()],
		"int": random.int(1, 1000000),
		"wide": random.int(-9223372036854775808, 9223372036854775807),
		"string": random.string(16),
		"given": inputs.tags.map(k, k),
		"built": {"z": 1, "y": 2, "x": 3, "w": 4, "v": 5, "u": 6}.map(k, k),
		"entries": {"b": 1, "a": 2}.transformMapEntry(k, v, {k: v}).map(k, k),
		"text": json.encode({"z": 1, "y": 2, "x": 3})
	}`
	req := expr.Request{Surface: "test", Form: expr.FormExpression, Source: src}
	first, err := r.Evaluate(t.Context(), req, input())
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, err := r.Evaluate(t.Context(), req, input())
		if err != nil || !reflect.DeepEqual(again.Value, first.Value) {
			t.Fatalf("a repeat gave %v, %v; want %v", again.Value, err, first.Value)
		}
	}
	got := first.Value.(map[string]any)
	if got["now"] != input().Now {
		t.Errorf("now = %v", got["now"])
	}
	for key, want := range map[string]any{
		"given":   []any{"app", "env", "team"},
		"built":   []any{"u", "v", "w", "x", "y", "z"},
		"entries": []any{"a", "b"},
		"text":    `{"x":3,"y":2,"z":1}`,
	} {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	uuid := got["uuid"].(string)
	if len(uuid) != 36 || uuid[14] != '4' || !strings.ContainsRune("89ab", rune(uuid[19])) {
		t.Errorf("random.uuid() = %s, not a version 4 UUID", uuid)
	}
	if n := got["int"].(int64); n < 1 || n > 1000000 {
		t.Errorf("random.int(1, 1000000) = %d", n)
	}
	if s := got["string"].(string); len(s) != 16 || strings.Trim(s,
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") != "" {
		t.Errorf("random.string(16) = %q", s)
	}
	if u := got["uuids"].([]any); u[0] == u[1] || u[0] == uuid {
		t.Errorf("one evaluation drew the same UUID twice: %v", u)
	}

	other := input()
	other.Seed++
	again, err := r.Evaluate(t.Context(), req, other)
	if err != nil || again.Value.(map[string]any)["uuid"] == uuid {
		t.Errorf("another seed drew %v, %v", again.Value, err)
	}
}

// TestUnitExprCostLimit runs comprehensions that would never end in
// practice and requires each to fail naming expressions.cost_limit; a
// template's expressions share one budget; and Check warns when the
// estimate alone is over the limit.
func TestUnitExprCostLimit(t *testing.T) {
	r := registry(t, expr.Defaults())
	const unbounded = `lists.range(1000000).map(i, lists.range(1000000).map(j, i * j))`
	for _, form := range []expr.Form{expr.FormExpression, expr.FormTemplate} {
		src := unbounded
		if form == expr.FormTemplate {
			src = "x ${{ " + unbounded + " }}"
		}
		_, err := eval(t, r, form, src)
		f, ok := errors.AsType[*expr.Failed](err)
		if !ok || f.Limit != "expressions.cost_limit" ||
			f.Message != "The evaluation went over expressions.cost_limit (1000000 units)." {
			t.Errorf("%s: %v", form, err)
		}
	}
	chk, err := r.Check(expr.Request{Surface: "test", Form: expr.FormExpression, Source: unbounded})
	if err != nil || !chk.OK() || len(chk.Diagnostics) != 1 ||
		chk.Diagnostics[0].Severity != expr.SeverityWarning ||
		!strings.Contains(chk.Diagnostics[0].Message, "over expressions.cost_limit (1000000)") ||
		chk.Diagnostics[0].Range != rng(1, 1, 1, len(unbounded)+1) || chk.Cost.Min <= 1000000 {
		t.Errorf("Check of an unbounded comprehension: %+v, %v", chk, err)
	}
	if chk, _ := r.Check(
		expr.Request{Surface: "test", Form: expr.FormExpression, Source: "1 + 1"},
	); chk.Cost.Min == 0 ||
		chk.Cost.Max < chk.Cost.Min ||
		chk.Type != "int" {
		t.Errorf("Check of 1 + 1: %+v", chk)
	}

	// The limit lets one part run, not two.
	const part = "${{ lists.range(20).map(i, i).size() }}"
	one, err := r.Check(expr.Request{Surface: "test", Form: expr.FormTemplate, Source: part})
	if err != nil || one.Cost.Max == 0 {
		t.Fatalf("Check of one part: %+v, %v", one, err)
	}
	tight := expr.Defaults()
	tight.CostLimit = one.Cost.Max * 3 / 2
	r = registry(t, tight)
	if _, err := eval(t, r, expr.FormTemplate, part); err != nil {
		t.Fatalf("one part under the limit: %v", err)
	}
	_, err = eval(t, r, expr.FormTemplate, part+part)
	if f, ok := errors.AsType[*expr.Failed](err); !ok || f.Limit != "expressions.cost_limit" {
		t.Errorf("two parts over the limit together: %v", err)
	}
}

// TestUnitExprTimeout interrupts an evaluation that would run far longer
// than expressions.timeout, naming the limit, and returns the caller's own
// cancellation as it is.
func TestUnitExprTimeout(t *testing.T) {
	cfg := expr.Defaults()
	cfg.CostLimit = ^uint64(0)
	cfg.Timeout = 20 * time.Millisecond
	r := registry(t, cfg)
	const slow = `lists.range(1000000).map(i, lists.range(1000000).map(j, i * j)).size()`
	start := time.Now()
	_, err := eval(t, r, expr.FormExpression, slow)
	f, ok := errors.AsType[*expr.Failed](err)
	if !ok || f.Limit != "expressions.timeout" ||
		f.Message != "The evaluation went over expressions.timeout (20ms)." {
		t.Errorf("an unbounded comprehension: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the interrupt took %s", took)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = r.Evaluate(
		ctx,
		expr.Request{Surface: "test", Form: expr.FormExpression, Source: slow},
		input(),
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled caller: %v", err)
	}
}

// TestUnitExprSchemaTypes maps JSON Schema fixtures to CEL types, coerces
// JSON numbers to the declared type, and refuses a misspelt field of a
// declared object at check time, with its range.
func TestUnitExprSchemaTypes(t *testing.T) {
	for _, c := range []struct{ schema, src, want string }{
		{`{"type": "string"}`, "v", "string"},
		{`{"type": "integer"}`, "v", "int"},
		{`{"type": "number"}`, "v", "double"},
		{`{"type": "boolean"}`, "v", "bool"},
		{`{"type": "null"}`, "v", "null"},
		{`{"type": "array", "items": {"type": "string"}}`, "v", "list(string)"},
		{`{"type": "array"}`, "v", "list(dyn)"},
		{`{"type": "object"}`, "v", "map(string, dyn)"},
		{`{"type": "object", "additionalProperties": true}`, "v", "map(string, dyn)"},
		{`{"type": "object", "additionalProperties": {"type": "integer"}}`, "v", "map(string, int)"},
		{`{"type": "object", "properties": {"a": {"type": "string"}}}`, "v", "v"},
		{`{"properties": {"a": {"type": "string"}}}`, "v.a", "string"},
		{
			`{"type": "object", "properties": {"a": {"type": "string"}}, "additionalProperties": true}`,
			"v", "map(string, dyn)",
		},
		{
			`{"type": "object", "properties": {"a": {"type": "string"}}, "additionalProperties": false}`,
			"v", "v",
		},
		{
			`{"type": "object", "properties": {"inner": {"type": "object", "properties": {"b": {"type": "integer"}}}}}`,
			"v.inner", "v/inner",
		},
		{
			`{"type": "array", "items": {"type": "object", "properties": {"b": {"type": "integer"}}}}`,
			"v[0].b", "int",
		},
		{`{"type": ["string", "null"]}`, "v", "dyn"},
		{`{"oneOf": [{"type": "string"}]}`, "v", "dyn"},
		{``, "v", "dyn"},
	} {
		r := registry(t, expr.Defaults(), expr.Surface{Name: "s", Variables: []expr.Variable{
			{Name: "v", Schema: json.RawMessage(c.schema)},
		}})
		chk, err := r.Check(expr.Request{Surface: "s", Form: expr.FormExpression, Source: c.src})
		if err != nil || !chk.OK() || chk.Type != c.want {
			t.Errorf("%s as %s: %+v, %v; want %s", c.src, c.schema, chk, err, c.want)
		}
	}

	r := registry(t, expr.Defaults())
	for _, c := range []struct {
		src  string
		want kernel.Range
	}{
		{"inputs.nmae", rng(1, 8, 1, 12)},
		{"inputs.subnets[0].cdir", rng(1, 19, 1, 23)},
		{"size(inputs.name) > 0 &&\n  inputs.tags.size() > inputs.cuont", rng(2, 31, 2, 36)},
	} {
		chk, err := r.Check(expr.Request{Surface: "test", Form: expr.FormExpression, Source: c.src})
		if err != nil || chk.OK() || len(chk.Diagnostics) != 1 ||
			!strings.HasPrefix(chk.Diagnostics[0].Message, "undefined field") ||
			chk.Diagnostics[0].Range != c.want {
			t.Errorf("%q: %+v, %v; want a misspelling at %+v", c.src, chk, err, c.want)
			continue
		}
		fe := chk.Diagnostics[0].FieldError("/value")
		if fe.Pointer != "/value" || fe.Reason != kernel.ReasonRule || fe.Range == nil ||
			*fe.Range != c.want {
			t.Errorf("%q as a field error: %+v", c.src, fe)
		}
	}

	for _, c := range []struct {
		src  string
		want any
	}{
		{"inputs.count + 1", int64(4)},
		{"inputs.ratio / 4.0", 0.5},
		{"type(inputs.count) == int && type(inputs.ratio) == double", true},
	} {
		in := input()
		values := in.Values["inputs"].(map[string]any)
		values["ratio"] = 2
		got, err := r.Evaluate(
			t.Context(),
			expr.Request{Surface: "test", Form: expr.FormExpression, Source: c.src},
			in,
		)
		if err != nil || !reflect.DeepEqual(got.Value, c.want) {
			t.Errorf("%s = %#v, %v; want %#v", c.src, got.Value, err, c.want)
		}
	}
	in := input()
	in.Values["inputs"].(map[string]any)["count"] = json.Number("7")
	in.Values["data"] = map[string]any{"n": json.Number("2.5"), "m": json.Number("2")}
	got, err := r.Evaluate(t.Context(), expr.Request{
		Surface: "test", Form: expr.FormExpression, Source: "[inputs.count, data.n, data.m]",
	}, in)
	if err != nil || !reflect.DeepEqual(got.Value, []any{int64(7), 2.5, int64(2)}) {
		t.Errorf("json.Number values: %#v, %v", got.Value, err)
	}
}

// TestUnitExprSecrets loads only the secret keys a source names with a
// literal, refuses any other use of a secret variable at check time, and
// masks a result or failure that read one.
func TestUnitExprSecrets(t *testing.T) {
	r := registry(t, expr.Defaults())
	var asked [][]string
	in := input()
	load := in.Secrets
	in.Secrets = func(ctx context.Context, variable string, keys []string) (map[string]string, error) {
		if variable != "secrets" {
			t.Errorf("loaded variable %s", variable)
		}
		asked = append(asked, keys)
		return load(ctx, variable, keys)
	}
	run := func(form expr.Form, src string) (expr.Result, error) {
		return r.Evaluate(t.Context(), expr.Request{Surface: "test", Form: form, Source: src}, in)
	}

	got, err := run(expr.FormExpression, `secrets.TOKEN + "-" + secrets["TOKEN"] + "/" +
		secrets.?OTHER.orValue("") + secrets[?"MISSING"].orValue("none") +
		string(has(secrets.TOKEN))`)
	if err != nil || !got.Secret ||
		got.Value != "tok-5ecret-value-tok-5ecret-value/other-secretnonetrue" ||
		got.Mask(got.Value.(string)) != "*****-*****/*****nonetrue" {
		t.Errorf("literal keys: %#v, %v", got, err)
	}
	if len(asked) != 1 || !slices.Equal(asked[0], []string{"TOKEN", "OTHER", "MISSING"}) {
		t.Errorf("loaded keys %v, want TOKEN, OTHER and MISSING once each", asked)
	}

	got, err = run(expr.FormTemplate, "Authorization: Bearer ${{ secrets.TOKEN }}")
	if err != nil || got.Mask(got.Value.(string)) != "Authorization: Bearer *****" {
		t.Errorf("a template reading a secret: %#v, %v", got, err)
	}
	if got, err := run(expr.FormExpression, `inputs.name`); err != nil || got.Secret ||
		got.Mask("tok-5ecret-value") != "tok-5ecret-value" {
		t.Errorf("a source reading no secret: %#v, %v", got, err)
	}

	_, err = run(expr.FormExpression, `fail("leaked " + secrets.TOKEN)`)
	if f, ok := errors.AsType[*expr.Failed](err); !ok || f.Message != "leaked *****" {
		t.Errorf("a failure naming a secret: %v", err)
	}
	_, err = run(expr.FormExpression, `secrets.NOPE`)
	if f, ok := errors.AsType[*expr.Failed](
		err,
	); !ok || !strings.Contains(f.Message, "no such key: NOPE") ||
		f.Range == nil ||
		*f.Range != rng(1, 9, 1, 13) {
		t.Errorf("a missing secret: %v", err)
	}

	for _, src := range []string{
		`secrets[inputs.name]`,
		`size(secrets)`,
		`secrets`,
		`secrets.map(k, k)`,
		`secrets[1 == 1 ? "TOKEN" : "OTHER"]`,
		`secrets[?inputs.name]`,
	} {
		chk, err := r.Check(expr.Request{Surface: "test", Form: expr.FormExpression, Source: src})
		if err != nil || chk.OK() ||
			!strings.Contains(chk.Diagnostics[0].Message, "only with a literal key") ||
			chk.Diagnostics[0].Range.Start != (kernel.Position{Line: 1, Column: strings.Index(src, "secrets") + 1}) {
			t.Errorf("%s: %+v, %v", src, chk, err)
		}
	}

	in.Secrets = nil
	if _, err := run(expr.FormExpression, `secrets.TOKEN`); !errors.Is(err, expr.ErrRequest) {
		t.Errorf("no secret loader: %v", err)
	}
	down := errors.New("secret store down")
	in.Secrets = func(context.Context, string, []string) (map[string]string, error) { return nil, down }
	if _, err := run(expr.FormExpression, `secrets.TOKEN`); !errors.Is(err, down) {
		t.Errorf("a failing secret loader: %v", err)
	}
}

// TestUnitExprRegistry checks the registry's surfaces and how it refuses
// requests it cannot compile, and that a cached program evaluates each
// input afresh, also concurrently.
func TestUnitExprRegistry(t *testing.T) {
	for _, c := range []struct {
		name     string
		cfg      expr.Config
		surfaces []expr.Surface
		want     error
	}{
		{"bad limits", expr.Config{}, nil, expr.ErrConfig},
		{"two contexts", expr.Defaults(), []expr.Surface{{Name: "a", Env: env}, {Name: "a", Env: env}}, expr.ErrRequest},
		{"a variable declared twice", expr.Defaults(), []expr.Surface{
			{Name: "a", Variables: []expr.Variable{{Name: "x"}}},
			{Name: "a", Variables: []expr.Variable{{Name: "x"}}},
		}, expr.ErrRequest},
		{"no name", expr.Defaults(), []expr.Surface{{}}, expr.ErrRequest},
		{
			"reserved now", expr.Defaults(),
			[]expr.Surface{{Name: "a", Variables: []expr.Variable{{Name: "now"}}}},
			expr.ErrRequest,
		},
		{"duplicate variable", expr.Defaults(), []expr.Surface{{Name: "a", Variables: []expr.Variable{
			{Name: "x"}, {Name: "x"},
		}}}, expr.ErrRequest},
	} {
		if _, err := expr.NewRegistry(c.cfg, c.surfaces...); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}

	b := surface()
	b.Name = "another"
	r := registry(t, expr.Defaults(), surface(), b)
	if got := r.Surfaces(); !slices.Equal(got, []string{"another", "test"}) {
		t.Errorf("Surfaces() = %v", got)
	}
	if s, ok := r.Surface("test"); !ok || len(s.Variables) != 3 {
		t.Errorf("Surface(test) = %+v, %v", s, ok)
	}
	if _, ok := r.Surface("nope"); ok {
		t.Error("an unknown surface was found")
	}
	added := expr.Surface{Name: "test", Variables: []expr.Variable{{Name: "run"}}}
	owned := surface()
	owned.Env = env
	for _, order := range [][]expr.Surface{{added, owned}, {owned, added}} {
		s, _ := registry(t, expr.Defaults(), order...).Surface("test")
		if len(s.Variables) != 4 || s.Env == nil {
			t.Errorf("a surface merged from %d declarations = %+v", len(order), s)
		}
	}

	for _, c := range []struct {
		name string
		req  expr.Request
		want error
	}{
		{
			"unknown surface",
			expr.Request{Surface: "nope", Form: expr.FormExpression, Source: "1"},
			expr.ErrUnknownSurface,
		},
		{"unknown form", expr.Request{Surface: "test", Form: "script", Source: "1"}, expr.ErrRequest},
		{"undeclared variable", expr.Request{
			Surface: "test", Form: expr.FormExpression, Source: "1",
			Variables: []expr.Variable{{Name: "extra"}},
		}, expr.ErrRequest},
		{"schema not JSON", expr.Request{
			Surface: "test", Form: expr.FormExpression, Source: "1",
			Variables: []expr.Variable{{Name: "data", Schema: json.RawMessage(`{`)}},
		}, expr.ErrRequest},
		{"schema not an object", expr.Request{
			Surface: "test", Form: expr.FormExpression, Source: "1",
			Variables: []expr.Variable{{Name: "data", Schema: json.RawMessage(`[1]`)}},
		}, expr.ErrRequest},
	} {
		if _, err := r.Check(c.req); !errors.Is(err, c.want) {
			t.Errorf("Check, %s: %v", c.name, err)
		}
		if _, err := r.Evaluate(t.Context(), c.req, input()); !errors.Is(err, c.want) {
			t.Errorf("Evaluate, %s: %v", c.name, err)
		}
	}

	narrowed := expr.Request{
		Surface: "test", Form: expr.FormExpression, Source: "data.y + data.zz",
		Variables: []expr.Variable{{Name: "data", Schema: json.RawMessage(
			`{"type": "object", "properties": {"y": {"type": "number"}, "z": {"type": "number"}}}`,
		)}},
	}
	chk, err := r.Check(narrowed)
	if err != nil || chk.OK() || chk.Diagnostics[0].Message != "undefined field 'zz'" {
		t.Errorf("a context's narrower schema: %+v, %v", chk, err)
	}
	_, err = r.Evaluate(t.Context(), narrowed, input())
	if ce, ok := errors.AsType[*expr.CheckError](err); !ok || len(ce.Diagnostics) != 1 ||
		err.Error() != "1:15: undefined field 'zz'" {
		t.Errorf("Evaluate of a source that does not compile: %v", err)
	}
	if got := (&expr.CheckError{}).Error(); got != "the source does not compile" {
		t.Errorf("an empty CheckError: %s", got)
	}

	small := expr.Defaults()
	small.MaxSource = 8
	chk, err = registry(t, small).Check(expr.Request{
		Surface: "test", Form: expr.FormTemplate,
		Source: "0123456789\nab",
	})
	if err != nil || chk.OK() || chk.Diagnostics[0].Message !=
		"The source is 13 bytes, over expressions.max_source (8)." ||
		chk.Diagnostics[0].Range != rng(1, 1, 2, 3) {
		t.Errorf("a source over max_source: %+v, %v", chk, err)
	}

	req := expr.Request{
		Surface: "test",
		Form:    expr.FormExpression,
		Source:  "inputs.name + string(inputs.count)",
	}
	done := make(chan error)
	for i := range 8 {
		go func() {
			in := input()
			in.Values["inputs"].(map[string]any)["count"] = float64(i)
			got, err := r.Evaluate(context.Background(), req, in)
			if err == nil && got.Value != "web"+string(rune('0'+i)) {
				err = errors.New("got " + got.Value.(string))
			}
			done <- err
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Errorf("a concurrent evaluation: %v", err)
		}
	}
}

// TestUnitExprReads checks that a check reports each literal read of a
// variable's key across a template's expressions, in source order with
// its range, and leaves out a key the source computes; and that keys
// closing a map refuse a literal read of another key, at check and at
// evaluation, leaving the cached check as it was.
func TestUnitExprReads(t *testing.T) {
	r := registry(t, expr.Defaults(), expr.Surface{Name: "s", Variables: []expr.Variable{
		{
			Name: "env",
			Schema: json.RawMessage(
				`{"type": "object", "additionalProperties": {"type": "string"}}`,
			),
		},
		{Name: "other", Schema: json.RawMessage(`{"type": "object"}`)},
	}})
	req := expr.Request{
		Surface: "s", Form: expr.FormTemplate,
		Source: "${{ env.B }}-${{ env[\"A\"] }}-${{ env.B + env[other.k] }}",
	}
	chk, err := r.Check(req)
	if err != nil || !chk.OK() {
		t.Fatalf("%+v, %v", chk, err)
	}
	if want := []expr.Read{
		{Variable: "env", Key: "B", Range: rng(1, 9, 1, 10)},
		{Variable: "env", Key: "A", Range: rng(1, 22, 1, 25)},
		{Variable: "env", Key: "B", Range: rng(1, 38, 1, 39)},
		{Variable: "other", Key: "k", Range: rng(1, 52, 1, 53)},
	}; !reflect.DeepEqual(chk.Reads, want) {
		t.Errorf("reads %+v, want %+v", chk.Reads, want)
	}
	chk.Reads[0].Key = "Z"
	if again, _ := r.Check(req); again.Reads[0].Key != "B" {
		t.Errorf("the cached check, its caller's copy changed, reads %+v", again.Reads)
	}

	closed := req
	closed.Keys = map[string][]string{"env": {"B"}}
	chk, err = r.Check(closed)
	if want := []expr.Diagnostic{{
		Severity: expr.SeverityError, Message: `env has no key "A"`, Range: rng(1, 22, 1, 25),
	}}; err != nil || !reflect.DeepEqual(chk.Diagnostics, want) {
		t.Errorf("a closed map's diagnostics %+v, %v", chk.Diagnostics, err)
	}
	if again, _ := r.Check(req); !again.OK() {
		t.Errorf("the cached check took the closed map's diagnostics: %+v", again.Diagnostics)
	}
	in := expr.Input{Values: map[string]any{
		"env": map[string]any{"A": "a", "B": "b"}, "other": map[string]any{"k": "A"},
	}}
	if _, err := r.Evaluate(t.Context(), closed, in); !isCheckError(err, 1) {
		t.Errorf("evaluating a read of a key the map is closed to = %v", err)
	}
	closed.Keys = map[string][]string{"env": {"A", "B"}}
	if res, err := r.Evaluate(t.Context(), closed, in); err != nil || res.Value != "b-a-ba" {
		t.Errorf("evaluating within the closed map = %+v, %v", res, err)
	}
}

// isCheckError reports whether err is a *expr.CheckError of n diagnostics.
func isCheckError(err error, n int) bool {
	ce, ok := errors.AsType[*expr.CheckError](err)
	return ok && len(ce.Diagnostics) == n
}

// TestUnitExprComplete checks the items at a cursor: variables and global
// functions at the start, an object's fields from its schema and a map's
// from the sample after a dot, a namespace's functions, the functions
// called on a value, and none outside a template's ${{ … }}.
func TestUnitExprComplete(t *testing.T) {
	r := registry(t, expr.Defaults(), expr.Surface{Name: "s", Variables: []expr.Variable{
		{
			Name: "env",
			Schema: json.RawMessage(
				`{"type": "object", "additionalProperties": {"type": "string"}}`,
			),
		},
		{Name: "stack", Schema: json.RawMessage(`{"type": "object", "properties": {
			"name": {"type": "string"}, "labels": {"type": "array", "items": {"type": "string"}}}}`)},
	}})
	sample := map[string]any{"env": map[string]any{"REGION": "eu", "ROLE": "x", "TOKEN": "*****"}}
	labels := func(items []expr.Completion) []string {
		out := make([]string, 0, len(items))
		for _, c := range items {
			out = append(out, c.Kind+":"+c.Label)
		}
		return out
	}
	complete := func(form expr.Form, src string, line, column int) []expr.Completion {
		t.Helper()
		items, err := r.Complete(expr.Request{Surface: "s", Form: form, Source: src},
			kernel.Position{Line: line, Column: column}, sample)
		if err != nil {
			t.Fatalf("%q at %d:%d: %v", src, line, column, err)
		}
		return items
	}
	items := complete(expr.FormTemplate, "x ${{ env.R", 1, 12)
	if got := labels(items); !reflect.DeepEqual(got, []string{"field:REGION", "field:ROLE"}) {
		t.Errorf("env.R completes %v", got)
	}
	if items[0].Range != rng(1, 11, 1, 12) || items[0].Detail != "string" {
		t.Errorf("env.R's first item %+v", items[0])
	}
	if got := labels(
		complete(expr.FormExpression, "stack.", 1, 7),
	); !slices.Contains(got, "field:labels") ||
		!slices.Contains(got, "field:name") ||
		!slices.Contains(got, "function:size") {
		t.Errorf("stack. completes %v", got)
	}
	if got := labels(complete(expr.FormExpression, "s", 1, 2)); !reflect.DeepEqual(got,
		[]string{"variable:stack", "function:size", "function:string"}) {
		t.Errorf("s completes %v", got)
	}
	if got := labels(
		complete(expr.FormExpression, "n", 1, 2),
	); !reflect.DeepEqual(
		got,
		[]string{"variable:now"},
	) {
		t.Errorf("n completes %v", got)
	}
	if got := labels(complete(expr.FormExpression, "base64.e", 1, 9)); !reflect.DeepEqual(got,
		[]string{"function:encode"}) {
		t.Errorf("base64.e completes %v", got)
	}
	if got := labels(
		complete(expr.FormExpression, "[1].al", 1, 7),
	); !slices.Contains(
		got,
		"macro:all",
	) {
		t.Errorf("[1].al completes %v", got)
	}
	if got := complete(expr.FormTemplate, "${{ env.A }} tail", 1, 15); len(got) != 0 {
		t.Errorf("outside an expression %v", got)
	}
	if got := complete(expr.FormTemplate, "$${{ env.", 1, 10); len(got) != 0 {
		t.Errorf("after an escaped ${{ %v", got)
	}
	if got := labels(
		complete(expr.FormExpression, "true &&\n  stack.na", 2, 11),
	); !reflect.DeepEqual(
		got,
		[]string{"field:name"},
	) {
		t.Errorf("on a second line %v", got)
	}
	if got := complete(
		expr.FormExpression,
		"nope.",
		1,
		6,
	); len(got) == 0 ||
		slices.Contains(labels(got), "field:name") {
		t.Errorf("after an unknown name %v", labels(got))
	}
	typed := registry(t, expr.Defaults(), expr.Surface{Name: "t", Variables: []expr.Variable{
		{Name: "v", Schema: json.RawMessage(`{"properties": {
			"i": {"type": "integer"}, "n": {"type": "number"}, "b": {"type": "boolean"},
			"z": {"type": "null"}, "l": {"type": "array"}, "o": {"properties": {"x": {}}},
			"m": {"type": "object"}, "d": {}}}`)},
	}})
	got, err := typed.Complete(expr.Request{Surface: "t", Form: expr.FormExpression, Source: "v."},
		kernel.Position{Line: 1, Column: 3}, nil)
	details := map[string]string{}
	for _, c := range got {
		if c.Kind == expr.KindField {
			details[c.Label] = c.Detail
		}
	}
	if want := map[string]string{
		"i": "int", "n": "double", "b": "bool", "z": "null", "l": "list(dyn)", "o": "object",
		"m": "map(string, dyn)", "d": "dyn",
	}; err != nil || !reflect.DeepEqual(details, want) {
		t.Errorf("v.'s fields %v, %v", details, err)
	}
	if got, err := typed.Complete(
		expr.Request{Surface: "t", Form: expr.FormExpression, Source: "v.o.x."},
		kernel.Position{
			Line:   1,
			Column: 7,
		},
		nil,
	); err != nil ||
		slices.ContainsFunc(got, func(c expr.Completion) bool {
			return c.Kind == expr.KindField
		}) {
		t.Errorf("a field of a value with none %v, %v", got, err)
	}

	for _, c := range []struct {
		req expr.Request
		at  kernel.Position
	}{
		{expr.Request{Surface: "s", Form: expr.FormExpression, Source: "env"}, kernel.Position{Line: 2, Column: 1}},
		{expr.Request{Surface: "s", Form: "yaml", Source: "env"}, kernel.Position{Line: 1, Column: 1}},
		{expr.Request{Surface: "nope", Form: expr.FormExpression, Source: "env"}, kernel.Position{Line: 1, Column: 1}},
	} {
		if _, err := r.Complete(c.req, c.at, nil); err == nil {
			t.Errorf("%+v at %+v completed", c.req, c.at)
		}
	}
}
