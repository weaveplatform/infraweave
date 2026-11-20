package http

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
)

// Policies lists full policy views in the caller's readable spaces.
func (s Server) Policies(ctx context.Context, req api.PoliciesRequestObject) (api.PoliciesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := search.Input{First: req.Params.Limit}
	if req.Params.Cursor != nil {
		in.After = *req.Params.Cursor
	}
	res, _, err := s.svc.Search(ctx, p, in, nil)
	if err != nil {
		return nil, policyError(err)
	}
	names, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, fmt.Errorf("policies: name list spaces: %w", err)
	}
	out := api.PolicyPage{Items: make([]api.Policy, 0, len(res.Edges))}
	for _, edge := range res.Edges {
		out.Items = append(out.Items, policyView(edge.Node, names))
	}
	if res.HasNext {
		out.NextCursor = &res.End
	}
	return api.Policies200JSONResponse{Body: out}, nil
}

// SearchPolicies returns account-scoped policy references and opaque cursors.
func (s Server) SearchPolicies(ctx context.Context, req api.SearchPoliciesRequestObject) (api.SearchPoliciesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, _, err := s.svc.Search(ctx, p, apisearch.Input(*req.Body), nil)
	if err != nil {
		return nil, policyError(err)
	}
	out := api.SearchPoliciesOutput{Edges: make([]api.PolicyConnection, 0, len(res.Edges)), PageInfo: apisearch.PageInfo(res)}
	for _, edge := range res.Edges {
		out.Edges = append(out.Edges, api.PolicyConnection{Cursor: edge.Cursor, Node: api.PolicyRef{Id: edge.Node.ID.String()}})
	}
	return api.SearchPolicies200JSONResponse{Body: out}, nil
}

// SearchPoliciesSuggestions counts only policy rows the caller can read.
func (s Server) SearchPoliciesSuggestions(ctx context.Context, req api.SearchPoliciesSuggestionsRequestObject) (api.SearchPoliciesSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.Suggest(ctx, p, apisearch.SuggestionsInput(*req.Body), nil)
	if err != nil {
		return nil, policyError(err)
	}
	return api.SearchPoliciesSuggestions200JSONResponse{Body: apisearch.Suggestions(out)}, nil
}

// SearchSpacePolicies returns the readable policies the space can inherit.
func (s Server) SearchSpacePolicies(ctx context.Context, req api.SearchSpacePoliciesRequestObject) (api.SearchSpacePoliciesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, space, err := s.svc.Search(ctx, p, apisearch.Input(req.Body.Input), &req.Body.Space)
	if err != nil {
		return nil, policyError(err)
	}
	out := api.SearchSpacePoliciesOutput{Edges: make([]api.SpacePolicyConnection, 0, len(res.Edges)), PageInfo: apisearch.PageInfo(res)}
	for _, edge := range res.Edges {
		out.Edges = append(out.Edges, api.SpacePolicyConnection{Cursor: edge.Cursor, Node: api.PolicyRef{Id: edge.Node.ID.String()}, Inherited: edge.Node.Space != space})
	}
	return api.SearchSpacePolicies200JSONResponse{Body: out}, nil
}

// SearchSpacePoliciesSuggestions uses the same inherited, readable scope.
func (s Server) SearchSpacePoliciesSuggestions(ctx context.Context, req api.SearchSpacePoliciesSuggestionsRequestObject) (api.SearchSpacePoliciesSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.Suggest(ctx, p, apisearch.SuggestionsInput(req.Body.Input), &req.Body.Space)
	if err != nil {
		return nil, policyError(err)
	}
	return api.SearchSpacePoliciesSuggestions200JSONResponse{Body: apisearch.Suggestions(out)}, nil
}
