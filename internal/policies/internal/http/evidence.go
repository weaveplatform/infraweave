package http

import (
	"context"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func sampleView(doc policyapi.Sample) api.PolicyEvaluationSample {
	out := api.PolicyEvaluationSample{
		Body: doc.Body, EngineType: api.PolicyEngineType(doc.Engine), Input: string(doc.Input),
		Results: string(doc.Result), Outcome: strings.ToLower(string(doc.Outcome)),
		Print: doc.Print, PrintTruncated: doc.PrintTruncated, Libraries: []api.SampledPolicyLibrary{},
	}
	if doc.Error != nil {
		out.Error = doc.Error.Message
	}
	return out
}

// PolicyEvaluationSample resolves an opaque evaluation handle inside its policy.
func (s Server) PolicyEvaluationSample(ctx context.Context, req api.PolicyEvaluationSampleRequestObject) (api.PolicyEvaluationSampleResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	policy, policyErr := kernel.ParseULID(req.PolicyId)
	evaluation, evaluationErr := kernel.ParseULID(req.Params.Key)
	if policyErr != nil || evaluationErr != nil {
		return nil, policyError(policyapi.ErrSampleMissing)
	}
	doc, err := s.svc.ReadSample(ctx, p, policy, evaluation)
	if err != nil {
		return nil, policyError(err)
	}
	return api.PolicyEvaluationSample200JSONResponse{Body: sampleView(doc)}, nil
}

// PolicySearchEvaluationRecords exposes only safe metadata and opaque handles.
func (s Server) PolicySearchEvaluationRecords(ctx context.Context, req api.PolicySearchEvaluationRecordsRequestObject) (api.PolicySearchEvaluationRecordsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.SearchEvaluations(ctx, p, req.PolicyId, apisearch.Input(req.Params.Input))
	if err != nil {
		return nil, policyError(err)
	}
	out := api.SearchPolicyEvaluationRecordOutput{Edges: []api.PolicyEvaluationRecordConnection{}, PageInfo: apisearch.PageInfo(res)}
	for _, edge := range res.Edges {
		out.Edges = append(
			out.Edges,
			api.PolicyEvaluationRecordConnection{
				Cursor: edge.Cursor,
				Node:   api.PolicyEvaluationRecord{Key: edge.Node.ID.String(), Outcome: edge.Node.Outcome, Timestamp: unix(edge.Node.At.Unix())},
			},
		)
	}
	return api.PolicySearchEvaluationRecords200JSONResponse{Body: out}, nil
}

// PolicySearchEvaluationRecordSuggestions uses the same metadata-only scope.
func (s Server) PolicySearchEvaluationRecordSuggestions(
	ctx context.Context,
	req api.PolicySearchEvaluationRecordSuggestionsRequestObject,
) (api.PolicySearchEvaluationRecordSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.SuggestEvaluations(ctx, p, req.PolicyId, apisearch.SuggestionsInput(req.Params.Input))
	if err != nil {
		return nil, policyError(err)
	}
	return api.PolicySearchEvaluationRecordSuggestions200JSONResponse{Body: apisearch.Suggestions(out)}, nil
}
