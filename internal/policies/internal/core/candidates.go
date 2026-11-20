package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// Candidates lists the readable policy programs a stack in the space may attach.
// Non-stack policy types are excluded before pagination, not from a fetched page.
func (s *Service) Candidates(ctx context.Context, p kernel.Principal, space string, in search.Input) (search.Result[Policy], error) {
	types := make([]string, len(stackTypes))
	for i, kind := range stackTypes {
		types[i] = string(kind)
	}
	in.Predicates = append(slices.Clone(in.Predicates), search.Predicate{Field: "type", Constraint: search.Constraint{EnumEquals: types}})
	out, _, err := s.Search(ctx, p, in, &space)
	return out, err
}

// StackCandidates resolves the actual stack space before applying the same
// readable inheritance scope and type filter as Candidates.
func (s *Service) StackCandidates(ctx context.Context, p kernel.Principal, stack string, in search.Input) (search.Result[Policy], error) {
	st, err := s.stack(ctx, p.AccountID, stack, false)
	if err != nil {
		return search.Result[Policy]{}, err
	}
	if err := s.readable(ctx, p, st.Space, "stack", stack); err != nil {
		return search.Result[Policy]{}, err
	}
	names, err := s.deps.Spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return search.Result[Policy]{}, fmt.Errorf("policies: name candidate space: %w", err)
	}
	slug, ok := names[st.Space]
	if !ok {
		return search.Result[Policy]{}, &Missing{Kind: "stack", ID: stack}
	}
	return s.Candidates(ctx, p, slug, in)
}
