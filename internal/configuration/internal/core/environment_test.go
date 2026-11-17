package core

import (
	"reflect"
	"testing"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
)

// value is an opened value for the tests.
type value string

func (v value) Reveal() []byte { return []byte(v) }

// vars are elements named and valued as pairs.
func vars(pairs ...string) []Opened {
	out := make([]Opened, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, Opened{Element: Element{Name: pairs[i]}, Secret: value(pairs[i+1])})
	}
	return out
}

// hooked are a phase's commands, each its source's name.
func hooked(name string) map[string][]string {
	return map[string][]string{"before_plan": {name}, "after_plan": {name}}
}

// TestUnitEnvironmentPrecedence checks the precedence of a stack's
// environment: the stack's own element outranks a context's, a context
// attached by hand one attached automatically, a lower priority a higher
// one, and among those attached automatically the last by name, then slug; a name
// the stack drops lets the next one through. Its hooks follow the worked
// example of auto-attached a and b, c at priority 0 and d at 5: c, d, b,
// a, stack before a phase and the reverse after it.
//
// ref: decision 0008 § "The run specification and its secrets"
func TestUnitEnvironmentPrecedence(t *testing.T) {
	stack := source{
		kind:     configapi.SourceStack,
		elements: vars("A", "stack"),
		hooks:    hooked("stack"),
	}
	attached := []source{
		{
			kind: configapi.SourceAutoattached, context: "context-b", name: "context_b",
			elements: vars("A", "b", "B", "b", "C", "b", "D", "b"), hooks: hooked("b"),
		},
		{
			kind: configapi.SourceContext, context: "context-d", name: "context_d", priority: 5,
			elements: vars("A", "d", "B", "d", "C", "d"), hooks: hooked("d"),
		},
		{
			kind: configapi.SourceAutoattached, context: "context-a", name: "context_a",
			elements: vars("A", "a", "B", "a", "C", "a", "D", "a", "E", "a"), hooks: hooked("a"),
		},
		{
			kind: configapi.SourceContext, context: "context-c", name: "context_c",
			elements: vars("A", "c", "B", "c"), hooks: hooked("c"),
		},
	}
	got := resolve(stack, attached)
	type won struct{ name, value, source, context string }
	elements := make([]won, 0, len(got.Elements))
	for _, e := range got.Elements {
		elements = append(elements, won{e.Name, string(e.Value.Reveal()), e.Source, e.Context})
	}
	if want := []won{
		{"A", "stack", configapi.SourceStack, ""},
		{"B", "c", configapi.SourceContext, "context-c"},
		{"C", "d", configapi.SourceContext, "context-d"},
		{"D", "b", configapi.SourceAutoattached, "context-b"},
		{"E", "a", configapi.SourceAutoattached, "context-a"},
	}; !reflect.DeepEqual(elements, want) {
		t.Errorf("elements %v, want %v", elements, want)
	}
	if want := map[string][]string{
		"before_plan": {"c", "d", "b", "a", "stack"},
		"after_plan":  {"stack", "a", "b", "d", "c"},
	}; !reflect.DeepEqual(got.Hooks, want) {
		t.Errorf("hooks %v, want %v", got.Hooks, want)
	}

	stack.elements = nil
	stack.hooks = nil
	got = resolve(stack, attached[:2])
	if len(got.Elements) != 4 || string(got.Elements[0].Value.Reveal()) != "d" ||
		got.Elements[0].Context != "context-d" {
		t.Errorf("with the stack's element deleted, %+v", got.Elements)
	}
	if want := map[string][]string{
		"before_plan": {"d", "b"},
		"after_plan":  {"b", "d"},
	}; !reflect.DeepEqual(
		got.Hooks,
		want,
	) {
		t.Errorf("hooks without the stack's %v, want %v", got.Hooks, want)
	}
	if got := resolve(source{kind: configapi.SourceStack}, nil); len(got.Elements) != 0 ||
		len(got.Hooks) != 0 {
		t.Errorf("an empty environment %+v", got)
	}
}

// TestUnitEnvironmentAutoattachedTie checks the descending slug tie-breaker
// for equal names, for both variables and mounted files, independently of
// the attachment read's order. Hooks follow that same rank and its reverse.
//
// ref: decision 0008 § "The run specification and its secrets"
func TestUnitEnvironmentAutoattachedTie(t *testing.T) {
	attached := []source{
		{kind: configapi.SourceAutoattached, context: "a-slug", name: "Shared", elements: vars("VALUE", "a", "config.txt", "a-file"), hooks: hooked("a")},
		{kind: configapi.SourceAutoattached, context: "z-slug", name: "Shared", elements: vars("VALUE", "z", "config.txt", "z-file"), hooks: hooked("z")},
	}
	for i := range attached {
		attached[i].elements[1].FileMount = true
	}
	for range 2 {
		got := resolve(source{kind: configapi.SourceStack}, attached)
		if len(got.Elements) != 2 {
			t.Fatalf("elements: %+v", got.Elements)
		}
		for _, e := range got.Elements {
			want := "z"
			if e.FileMount {
				want = "z-file"
			}
			if e.Context != "z-slug" || string(e.Value.Reveal()) != want {
				t.Errorf("same-name context winner: %+v", e)
			}
		}
		if want := map[string][]string{"before_plan": {"z", "a"}, "after_plan": {"a", "z"}}; !reflect.DeepEqual(got.Hooks, want) {
			t.Errorf("same-name hooks: %v, want %v", got.Hooks, want)
		}
		attached[0], attached[1] = attached[1], attached[0]
	}
}
