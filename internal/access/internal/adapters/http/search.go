package http

import (
	"math"

	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// searchInput is the reference's SearchInput as kernel/search reads it.
func searchInput(in api.SearchInput) search.Input {
	out := search.Input{
		First:      in.First,
		After:      deref(in.After),
		FullText:   deref(in.FullTextSearch),
		Predicates: predicates(in.Predicates),
	}
	if in.RequestedPage != nil {
		out.Page = search.Page(*in.RequestedPage)
	}
	if in.OrderBy != nil {
		for _, o := range *in.OrderBy {
			out.OrderBy = append(out.OrderBy, search.Order{
				Field: o.Field,
				Desc:  o.Direction != nil && *o.Direction == "DESC",
			})
		}
	}
	return out
}

// suggestionsInput is the reference's SearchSuggestionsInput as
// kernel/search reads it.
func suggestionsInput(in api.SearchSuggestionsInput) search.SuggestionsInput {
	out := search.SuggestionsInput{
		FullText:   deref(in.FullTextSearch),
		Predicates: predicates(in.Predicates),
	}
	if in.Fields != nil {
		out.Fields = *in.Fields
	}
	return out
}

func predicates(in *[]api.SearchQueryPredicate) []search.Predicate {
	if in == nil {
		return nil
	}
	out := make([]search.Predicate, len(*in))
	for i, p := range *in {
		c := p.Constraint
		k := search.Constraint{
			BooleanEquals:   derefOr(c.BooleanEquals),
			EnumEquals:      derefOr(c.EnumEquals),
			StringEquals:    derefOr(c.StringEquals),
			StringMatches:   derefOr(c.StringMatches),
			HierarchyValues: derefOr(c.HierarchyNodeValueEquals),
		}
		if c.HierarchyNode != nil {
			k.Hierarchy = []search.Node{}
			for _, n := range c.HierarchyNode.Nodes {
				k.Hierarchy = append(k.Hierarchy, search.Node{
					Equals: n.Equals, Descendants: n.IncludeDescendants,
				})
			}
		}
		if r := c.TimeInRange; r != nil {
			k.TimeInRange = &search.TimeRange{Start: r.Start, End: r.End}
		}
		if c.TimeInLast != nil {
			k.TimeInLast = search.Window(*c.TimeInLast)
		}
		out[i] = search.Predicate{
			Field: p.Field, Exclude: p.Exclude != nil && *p.Exclude, Constraint: k,
		}
	}
	return out
}

// derefOr is the list p points to, or nil: a list that is present but
// empty stays set, so it keeps no row.
func derefOr[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	if *p == nil {
		return []T{}
	}
	return *p
}

// suggestions is s as the reference's SearchSuggestionsOutput: every
// field filters, and a field's values are listed only when asked for.
func suggestions(s search.Suggestions) api.SearchSuggestionsOutput {
	out := api.SearchSuggestionsOutput{
		FilteredCount: count(s.Count), Fields: []api.SearchSuggestionsField{},
	}
	for _, sg := range s.Fields {
		t := api.SearchSuggestionsFieldType(sg.Field.Type)
		f := api.SearchSuggestionsField{
			Name: sg.Field.Name, Filterable: true, Orderable: sg.Field.Orderable, Type: &t,
		}
		if sg.Listed {
			n := counts(sg.Counts)
			switch sg.Field.Type {
			case search.Boolean:
				values := make([]bool, len(sg.Values))
				for i, v := range sg.Values {
					values[i] = v == "true"
				}
				f.PossibleValuesBoolean = &api.SearchSuggestionsPossibleValuesBoolean{
					Values: values, Counts: n,
				}
			case search.Enum:
				f.PossibleValuesEnum = &api.SearchSuggestionsPossibleValuesEnum{
					Values: sg.Values, Counts: n,
				}
			case search.String:
				f.PossibleValuesString = &api.SearchSuggestionsPossibleValuesString{
					Values: sg.Values, Counts: n,
				}
			case search.Hierarchy:
				nodes := make([]api.SearchSuggestionsPossibleValuesHierarchyNode, len(sg.Nodes))
				for i, n := range sg.Nodes {
					nodes[i] = api.SearchSuggestionsPossibleValuesHierarchyNode{
						Id: n.ID, DisplayValue: n.Display,
					}
					if n.Parent != "" {
						nodes[i].ParentId = &n.Parent
					}
				}
				direct := counts(sg.Direct)
				f.PossibleValuesHierarchy = &api.SearchSuggestionsPossibleValuesHierarchy{
					Values: nodes, Counts: n, DirectCounts: &direct,
				}
			case search.Time:
				f.PossibleValuesTime = new(true)
			}
		}
		out.Fields = append(out.Fields, f)
	}
	return out
}

// count is n as the reference's int32, which no count of rows exceeds.
func count(n int) int32 { return int32(min(n, math.MaxInt32)) } //nolint:gosec // clamped above

func counts(ns []int) []int32 {
	out := make([]int32, len(ns))
	for i, n := range ns {
		out[i] = count(n)
	}
	return out
}
