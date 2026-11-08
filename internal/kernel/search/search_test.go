package search

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

var fields = []Field{
	{Name: "name", Type: String, Orderable: true},
	{Name: "locked", Type: Boolean},
	{Name: "state", Type: Enum, Values: []string{"FINISHED", "FAILED"}},
	{Name: "space", Type: Hierarchy},
	{Name: "createdAt", Type: Time, Orderable: true},
}

func TestUnitCheck(t *testing.T) {
	end := int32(5)
	for _, tc := range []struct {
		name string
		in   Input
		want []FieldError
	}{
		{name: "empty", in: Input{}},
		{name: "every constraint that applies", in: Input{
			Predicates: []Predicate{
				{Field: "name", Constraint: Constraint{StringEquals: []string{"a"}}},
				{Field: "name", Constraint: Constraint{StringMatches: []string{"a*"}}},
				{Field: "locked", Exclude: true, Constraint: Constraint{BooleanEquals: []bool{true}}},
				{Field: "state", Constraint: Constraint{EnumEquals: []string{"FAILED"}}},
				{Field: "space", Constraint: Constraint{HierarchyValues: []string{"x"}}},
				{Field: "space", Constraint: Constraint{Hierarchy: []Node{{Equals: "x"}}}},
				{Field: "createdAt", Constraint: Constraint{TimeInRange: &TimeRange{End: &end}}},
				{Field: "createdAt", Constraint: Constraint{TimeInLast: "LAST1H"}},
			},
			OrderBy: []Order{{Field: "createdAt", Desc: true}, {Field: "name"}},
			Page:    Next, After: "c",
		}},
		{name: "unknown field", in: Input{Predicates: []Predicate{
			{Field: "nope", Constraint: Constraint{StringEquals: []string{}}},
		}}, want: []FieldError{{"/predicates/0/field", ReasonUnknownField, `The search has no field "nope".`}}},
		{name: "constraint of another type", in: Input{Predicates: []Predicate{
			{Field: "name", Constraint: Constraint{StringEquals: []string{}}},
			{Field: "name", Constraint: Constraint{BooleanEquals: []bool{true}}},
		}}, want: []FieldError{{
			"/predicates/1/constraint/booleanEquals", ReasonRule,
			"booleanEquals does not apply to name, a STRING field.",
		}}},
		{
			name: "no constraint", in: Input{Predicates: []Predicate{{Field: "name"}}},
			want: []FieldError{{
				"/predicates/0/constraint", ReasonRule,
				"A constraint sets exactly one member; this one sets 0.",
			}},
		},
		{name: "two constraints", in: Input{Predicates: []Predicate{{Field: "name", Constraint: Constraint{
			StringEquals: []string{"a"}, StringMatches: []string{"b"},
		}}}}, want: []FieldError{{
			"/predicates/0/constraint", ReasonRule,
			"A constraint sets exactly one member; this one sets 2.",
		}}},
		{name: "enum value", in: Input{Predicates: []Predicate{
			{Field: "state", Constraint: Constraint{EnumEquals: []string{"FAILED", "LOST"}}},
		}}, want: []FieldError{{
			"/predicates/0/constraint/enumEquals/1", ReasonEnum,
			`"LOST" is not a value of state.`,
		}}},
		{name: "window", in: Input{Predicates: []Predicate{
			{Field: "createdAt", Constraint: Constraint{TimeInLast: "LAST2Y"}},
		}}, want: []FieldError{{
			"/predicates/0/constraint/timeInLast", ReasonEnum,
			`"LAST2Y" is not a time window.`,
		}}},
		{name: "orders", in: Input{OrderBy: []Order{
			{Field: "gone"}, {Field: "locked"}, {Field: "name"}, {Field: "name", Desc: true},
		}}, want: []FieldError{
			{"/orderBy/0/field", ReasonUnknownField, `The search has no field "gone".`},
			{"/orderBy/1/field", ReasonRule, "The field locked does not order a search."},
			{"/orderBy/3/field", ReasonDuplicate, "The search is already ordered by name."},
		}},
		{
			name: "previous without a cursor", in: Input{Page: Previous},
			want: []FieldError{{"/after", ReasonRequired, "The PREVIOUS page is read from a cursor."}},
		},
		{name: "last ignores the cursor", in: Input{Page: Last, After: "c"}},
		{
			name: "unknown page", in: Input{Page: "MIDDLE"},
			want: []FieldError{{"/requestedPage", ReasonEnum, `"MIDDLE" is not a page.`}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Check(fields, tc.in)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Check = %v, want nil", err)
				}
				return
			}
			inv, ok := errors.AsType[*Invalid](err)
			if !ok {
				t.Fatalf("Check = %v, want *Invalid", err)
			}
			if !reflect.DeepEqual(inv.Errors, tc.want) {
				t.Errorf("Check errors = %+v, want %+v", inv.Errors, tc.want)
			}
			if inv.Error() != "search: "+tc.want[0].Pointer+": "+tc.want[0].Message {
				t.Errorf("Error() = %q", inv.Error())
			}
		})
	}
}

func TestUnitCheckSuggestions(t *testing.T) {
	if err := CheckSuggestions(fields, SuggestionsInput{Fields: []string{"nope"}}); err != nil {
		t.Errorf("an unknown suggested field is ignored, got %v", err)
	}
	err := CheckSuggestions(fields, SuggestionsInput{Predicates: []Predicate{{Field: "nope"}}})
	if inv, ok := errors.AsType[*Invalid](
		err,
	); !ok ||
		inv.Errors[0].Pointer != "/predicates/0/field" {
		t.Errorf("CheckSuggestions = %v, want the predicate's field refused", err)
	}
}

func TestUnitKind(t *testing.T) {
	for c, want := range map[*Constraint]string{
		{}:                        "",
		{BooleanEquals: []bool{}}: "booleanEquals",
		{Hierarchy: []Node{}}:     "hierarchyNode",
		{TimeInLast: "LAST1D"}:    "timeInLast",
		{StringEquals: []string{}, TimeInLast: "LAST1D"}: "",
	} {
		if got := c.Kind(); got != want {
			t.Errorf("Kind(%+v) = %q, want %q", *c, got, want)
		}
	}
}

func TestUnitWindow(t *testing.T) {
	if d, ok := Window("LAST14D").Duration(); !ok || d != 14*24*time.Hour {
		t.Errorf("LAST14D = %v, %v", d, ok)
	}
	if _, ok := Window("LAST2D").Duration(); ok {
		t.Error("LAST2D is a window")
	}
}

func TestUnitLookup(t *testing.T) {
	if f, ok := Lookup(fields, "state"); !ok || f.Type != Enum {
		t.Errorf("Lookup(state) = %+v, %v", f, ok)
	}
	if _, ok := Lookup(fields, "nope"); ok {
		t.Error("Lookup(nope) found a field")
	}
}
