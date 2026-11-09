package apisearch

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// TestUnitSuggestions converts a suggestion of each field type.
func TestUnitSuggestions(t *testing.T) {
	got := Suggestions(search.Suggestions{Count: 3, Fields: []search.Suggestion{
		{
			Field: search.Field{Name: "b", Type: search.Boolean}, Listed: true,
			Values: []string{"true", "false"}, Counts: []int{2, 1},
		},
		{
			Field: search.Field{Name: "e", Type: search.Enum}, Listed: true,
			Values: []string{"UP"}, Counts: []int{3},
		},
		{
			Field:  search.Field{Name: "h", Type: search.Hierarchy},
			Listed: true,
			Nodes: []search.HierarchyNode{
				{ID: "a", Display: "A"},
				{ID: "b", Parent: "a", Display: "B"},
			},
			Counts: []int{3, 1},
			Direct: []int{2, 1},
		},
		{Field: search.Field{Name: "t", Type: search.Time, Orderable: true}, Listed: true},
		{Field: search.Field{Name: "s", Type: search.String}},
	}})
	f := got.Fields
	if got.FilteredCount != 3 || len(f) != 5 ||
		!slices.Equal(f[0].PossibleValuesBoolean.Values, []bool{true, false}) ||
		!slices.Equal(f[0].PossibleValuesBoolean.Counts, []int32{2, 1}) ||
		!slices.Equal(f[1].PossibleValuesEnum.Values, []string{"UP"}) ||
		f[2].PossibleValuesHierarchy.Values[0].ParentId != nil ||
		*f[2].PossibleValuesHierarchy.Values[1].ParentId != "a" ||
		!slices.Equal(*f[2].PossibleValuesHierarchy.DirectCounts, []int32{2, 1}) ||
		!*f[3].PossibleValuesTime || !f[3].Orderable || *f[3].Type != "TIME" ||
		f[4].PossibleValuesString != nil || !f[4].Filterable {
		t.Errorf("suggestions = %+v", got)
	}
	if count(math.MaxInt32+1) != math.MaxInt32 {
		t.Error("count does not clamp")
	}
}

// TestUnitSearchInput converts every member of the reference's input.
func TestUnitSearchInput(t *testing.T) {
	var in api.SearchInput
	if err := json.Unmarshal([]byte(`{"first":5,"after":"c","fullTextSearch":"x",
		"requestedPage":"PREVIOUS","orderBy":[{"field":"name"},{"field":"id","direction":"DESC"}],
		"predicates":[
			{"field":"a","exclude":true,"constraint":{"booleanEquals":[true]}},
			{"field":"b","constraint":{"stringEquals":[]}},
			{"field":"c","constraint":{"hierarchyNode":{"nodes":[{"equals":"x","includeDescendants":true}]}}},
			{"field":"d","constraint":{"timeInRange":{"start":1}}},
			{"field":"e","constraint":{"timeInLast":"LAST1D"}},
			{"field":"f","constraint":{"enumEquals":["UP"],"stringMatches":["m*"],
				"hierarchyNodeValueEquals":["y"]}}]}`), &in); err != nil {
		t.Fatal(err)
	}
	want := search.Input{
		First: new(int32(5)), After: "c", FullText: "x", Page: search.Previous,
		OrderBy: []search.Order{{Field: "name"}, {Field: "id", Desc: true}},
		Predicates: []search.Predicate{
			{Field: "a", Exclude: true, Constraint: search.Constraint{BooleanEquals: []bool{true}}},
			{Field: "b", Constraint: search.Constraint{StringEquals: []string{}}},
			{Field: "c", Constraint: search.Constraint{
				Hierarchy: []search.Node{{Equals: "x", Descendants: true}},
			}},
			{
				Field:      "d",
				Constraint: search.Constraint{TimeInRange: &search.TimeRange{Start: new(int32(1))}},
			},
			{Field: "e", Constraint: search.Constraint{TimeInLast: "LAST1D"}},
			{Field: "f", Constraint: search.Constraint{
				EnumEquals: []string{
					"UP",
				},
				StringMatches:   []string{"m*"},
				HierarchyValues: []string{"y"},
			}},
		},
	}
	if got := Input(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Input =\n%+v\nwant\n%+v", got, want)
	}
	fields := []string{"id"}
	got := SuggestionsInput(api.SearchSuggestionsInput{Fields: &fields})
	if !slices.Equal(got.Fields, fields) || got.Predicates != nil {
		t.Errorf("SuggestionsInput = %+v", got)
	}
	var none *[]string
	if derefOr(none) != nil || derefOr(&[]string{}) == nil {
		t.Error("derefOr does not keep an empty list set")
	}
}

// TestUnitPageInfo answers a page's cursors and neighbours.
func TestUnitPageInfo(t *testing.T) {
	got := PageInfo(search.Result[int]{Start: "s", End: "e", HasNext: true})
	if got != (api.PageInfo{StartCursor: "s", EndCursor: "e", HasNextPage: true}) {
		t.Errorf("PageInfo = %+v", got)
	}
}
