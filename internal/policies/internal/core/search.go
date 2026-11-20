package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// Fields are the persisted policy settings searches can filter and order.
var Fields = []search.Field{
	{Name: "name", Type: search.String, Orderable: true},
	{Name: "description", Type: search.String},
	{Name: "label", Type: search.String},
	{Name: "space", Type: search.Hierarchy},
	{Name: "type", Type: search.Enum, Values: []string{"APPROVAL", "GIT_PUSH", "INITIALIZATION", "INTENT", "LOGIN", "NOTIFICATION", "PLAN", "TASK", "TRIGGER"}},
	{Name: "engineType", Type: search.Enum, Values: []string{"REGO_V0", "REGO_V1"}},
	{Name: "createdAt", Type: search.Time, Orderable: true},
	{Name: "updatedAt", Type: search.Time, Orderable: true},
}

// Scope confines a search before pagination and facet counts are calculated.
type Scope struct {
	Reachable []kernel.ULID
	Spaces    []accessapi.SpaceNode
}

// Search finds the caller's readable policies, or those reachable from a named
// space when space is supplied. The returned space identifies inherited edges.
func (s *Service) Search(ctx context.Context, p kernel.Principal, in search.Input, space *string) (search.Result[Policy], kernel.ULID, error) {
	scope, selected, err := s.scope(ctx, p, space)
	if err != nil {
		return search.Result[Policy]{}, selected, err
	}
	out, err := s.deps.Store.Search(ctx, p.AccountID, in, scope)
	return out, selected, err //nolint:wrapcheck // the store names the search
}

// Suggest counts the same readable rows and space inheritance as Search.
func (s *Service) Suggest(ctx context.Context, p kernel.Principal, in search.SuggestionsInput, space *string) (search.Suggestions, error) {
	scope, _, err := s.scope(ctx, p, space)
	if err != nil {
		return search.Suggestions{}, err
	}
	return s.deps.Store.Suggest(ctx, p.AccountID, in, scope) //nolint:wrapcheck // the store names the search
}

func (s *Service) scope(ctx context.Context, p kernel.Principal, slug *string) (Scope, kernel.ULID, error) {
	var out Scope
	var selected kernel.ULID
	var err error
	if out.Reachable, err = s.deps.Authz.Reachable(ctx, p, accessapi.ActionSpaceRead); err != nil {
		return out, selected, fmt.Errorf("policies: readable spaces: %w", err)
	}
	if out.Spaces, err = s.deps.Spaces.Tree(ctx, p.AccountID); err != nil {
		return out, selected, fmt.Errorf("policies: space tree: %w", err)
	}
	if slug == nil {
		return out, selected, nil
	}
	selected, err = s.deps.Spaces.Find(ctx, p.AccountID, *slug)
	if errors.Is(err, accessapi.ErrNoSpace) || err == nil && !slices.Contains(out.Reachable, selected) {
		return Scope{}, selected, &Missing{Kind: "space", ID: *slug}
	}
	if err != nil {
		return Scope{}, selected, fmt.Errorf("policies: search space: %w", err)
	}
	byID := make(map[kernel.ULID]accessapi.SpaceNode, len(out.Spaces))
	for _, node := range out.Spaces {
		byID[node.ID] = node
	}
	reach := []kernel.ULID{selected}
	for node := byID[selected]; node.Inherit && !node.Parent.IsZero(); node = byID[node.Parent] {
		reach = append(reach, node.Parent)
	}
	out.Reachable = slices.DeleteFunc(out.Reachable, func(id kernel.ULID) bool { return !slices.Contains(reach, id) })
	return out, selected, nil
}
