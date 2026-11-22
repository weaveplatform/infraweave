package pg_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/policies/internal/catalogue"
)

func TestAccCatalogueSharedTypedSearchPreservesCompletePinnedMembership(t *testing.T) {
	f := newFixture(t)
	rows, err := catalogue.All()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	limit := int32(7)
	in := search.Input{First: &limit}
	for page := 0; page < 10; page++ {
		out, err := f.store.SearchTemplates(t.Context(), f.principal.AccountID, rows, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range out.Edges {
			if seen[edge.Node.ID] {
				t.Fatal("repeated opaque template ID", edge.Node.ID)
			}
			if _, err := catalogue.Get(edge.Node.ID); err != nil {
				t.Fatal("unserved template returned", err)
			}
			seen[edge.Node.ID] = true
		}
		if !out.HasNext {
			break
		}
		if len(out.Edges) != int(limit) {
			t.Fatal("incomplete nonfinal template page")
		}
		in.After = out.End
	}
	if len(seen) != 62 {
		t.Fatalf("template pagination lost membership: %d", len(seen))
	}
	for kind, want := range map[string]int{"PLAN": 28, "GIT_PUSH": 14, "APPROVAL": 8, "NOTIFICATION": 7, "TRIGGER": 5, "LOGIN": 0, "TASK": 0, "INITIALIZATION": 0} {
		out, err := f.store.SuggestTemplates(
			t.Context(),
			f.principal.AccountID,
			rows,
			search.SuggestionsInput{Predicates: []search.Predicate{{Field: "type", Constraint: search.Constraint{EnumEquals: []string{kind}}}}},
		)
		if err != nil || out.Count != want {
			t.Fatalf("type %s count %d want %d: %v", kind, out.Count, want, err)
		}
	}
	filters := []search.Predicate{
		{Field: "label", Constraint: search.Constraint{StringEquals: []string{"approval"}}},
		{Field: "label", Constraint: search.Constraint{StringEquals: []string{"private"}}},
	}
	out, err := f.store.SearchTemplates(t.Context(), f.principal.AccountID, rows, search.Input{Predicates: filters, FullText: "worker"})
	if err != nil || len(out.Edges) != 1 {
		t.Fatalf("label/text filter: %+v %v", out, err)
	}
	suggestions, err := f.store.SuggestTemplates(t.Context(), f.principal.AccountID, rows, search.SuggestionsInput{Predicates: filters, FullText: "worker"})
	if err != nil || suggestions.Count != 1 {
		t.Fatalf("search/facet scope differs: %+v %v", suggestions, err)
	}
	for _, field := range suggestions.Fields {
		if field.Field.Name == "label" && len(field.Values) == 0 {
			t.Fatal("recordset array labels did not reach facets")
		}
	}
	// Identical global membership is visible to each authorized account; no
	// account-owned mutable policy row or raw source enters the query dataset.
	other, err := f.store.SuggestTemplates(t.Context(), kernel.NewAccountID(), rows, search.SuggestionsInput{})
	if err != nil || other.Count != 62 {
		t.Fatalf("account scope adapter lost public membership: %d %v", other.Count, err)
	}
	if _, err := f.store.SearchTemplates(t.Context(), f.principal.AccountID, rows, search.Input{After: "bad"}); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	if _, err := f.store.SearchTemplates(
		t.Context(),
		f.principal.AccountID,
		rows,
		search.Input{Predicates: []search.Predicate{{Field: "source", Constraint: search.Constraint{StringEquals: []string{"private"}}}}},
	); err == nil {
		t.Fatal("unserved source field searched")
	}
	if _, err := catalogue.Get("unknown"); !errors.Is(err, catalogue.ErrMissing) {
		t.Fatal("unknown catalogue handle accepted")
	}
}
