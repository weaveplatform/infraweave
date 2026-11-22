package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
)

// PolicyTemplate serves one pinned example and its supported import dialect.
func (s Server) PolicyTemplate(ctx context.Context, req api.PolicyTemplateRequestObject) (api.PolicyTemplateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.svc.Template(ctx, p, req.PolicyTemplateId)
	if err != nil {
		return nil, policyError(err)
	}
	return api.PolicyTemplate200JSONResponse{
		Body: api.PolicyTemplate{
			Id:          row.ID,
			Name:        row.Name,
			Slug:        row.Slug,
			Description: &row.Description,
			Labels:      row.Labels,
			Body:        row.Body,
			Type:        api.PolicyType(row.Type),
			EngineType:  api.PolicyEngineType(row.Engine),
		},
	}, nil
}

// SearchPolicyTemplates returns supported catalogue references and cursors.
func (s Server) SearchPolicyTemplates(ctx context.Context, req api.SearchPolicyTemplatesRequestObject) (api.SearchPolicyTemplatesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.SearchTemplates(ctx, p, apisearch.Input(*req.Body))
	if err != nil {
		return nil, policyError(err)
	}
	out := api.SearchPolicyTemplatesOutput{Edges: []api.PolicyTemplateConnection{}, PageInfo: apisearch.PageInfo(res)}
	for _, edge := range res.Edges {
		out.Edges = append(out.Edges, api.PolicyTemplateConnection{Cursor: edge.Cursor, Node: api.PolicyTemplateRef{Id: edge.Node.ID}})
	}
	return api.SearchPolicyTemplates200JSONResponse{Body: out}, nil
}

// SearchPolicyTemplatesSuggestions counts the same pinned membership as search.
func (s Server) SearchPolicyTemplatesSuggestions(
	ctx context.Context,
	req api.SearchPolicyTemplatesSuggestionsRequestObject,
) (api.SearchPolicyTemplatesSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.SuggestTemplates(ctx, p, apisearch.SuggestionsInput(*req.Body))
	if err != nil {
		return nil, policyError(err)
	}
	return api.SearchPolicyTemplatesSuggestions200JSONResponse{Body: apisearch.Suggestions(out)}, nil
}
