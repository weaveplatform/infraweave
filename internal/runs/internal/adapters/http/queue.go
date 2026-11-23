package http

import (
	"context"
	"math"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// WorkerPoolSearchSchedulableRuns searches the runs waiting for a private
// pool's workers on the stacks in the spaces the caller may read. The
// route has decided SPACE_READ on the pool's space, and answered 404 for
// a pool the caller cannot read.
func (s Server) WorkerPoolSearchSchedulableRuns(
	ctx context.Context,
	req api.WorkerPoolSearchSchedulableRunsRequestObject,
) (api.WorkerPoolSearchSchedulableRunsResponseObject, error) {
	out, err := s.searchQueue(ctx, poolQueue(req.WorkerPoolId), apisearch.Input(req.Params.Input))
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolSearchSchedulableRuns200JSONResponse{Body: out}, nil
}

// WorkerPoolSearchSchedulableRunsSuggestions lists the values the runs
// waiting for a private pool's workers hold.
func (s Server) WorkerPoolSearchSchedulableRunsSuggestions(
	ctx context.Context,
	req api.WorkerPoolSearchSchedulableRunsSuggestionsRequestObject,
) (api.WorkerPoolSearchSchedulableRunsSuggestionsResponseObject, error) {
	out, err := s.suggestQueue(ctx, poolQueue(req.WorkerPoolId), apisearch.SuggestionsInput(req.Params.Input))
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolSearchSchedulableRunsSuggestions200JSONResponse{Body: out}, nil
}

// PublicWorkerPoolSearchSchedulableRuns searches the caller's account's
// runs a public worker may claim, on the stacks in the spaces the caller
// may read.
func (s Server) PublicWorkerPoolSearchSchedulableRuns(
	ctx context.Context,
	req api.PublicWorkerPoolSearchSchedulableRunsRequestObject,
) (api.PublicWorkerPoolSearchSchedulableRunsResponseObject, error) {
	out, err := s.searchQueue(ctx, read.Queue{}, apisearch.Input(req.Params.Input))
	if err != nil {
		return nil, err
	}
	return api.PublicWorkerPoolSearchSchedulableRuns200JSONResponse{Body: out}, nil
}

// PublicWorkerPoolSearchSchedulableRunsSuggestions lists the values the
// runs a public worker may claim hold.
func (s Server) PublicWorkerPoolSearchSchedulableRunsSuggestions(
	ctx context.Context,
	req api.PublicWorkerPoolSearchSchedulableRunsSuggestionsRequestObject,
) (api.PublicWorkerPoolSearchSchedulableRunsSuggestionsResponseObject, error) {
	out, err := s.suggestQueue(ctx, read.Queue{}, apisearch.SuggestionsInput(req.Params.Input))
	if err != nil {
		return nil, err
	}
	return api.PublicWorkerPoolSearchSchedulableRunsSuggestions200JSONResponse{Body: out}, nil
}

// poolQueue is the queue of the pool id names. The route resolved id to a
// pool of the caller's account, so it parses.
func poolQueue(id string) read.Queue {
	pool, _ := kernel.ParseULID(id)
	return read.Queue{Pool: &pool}
}

func (s Server) searchQueue(ctx context.Context, q read.Queue, in search.Input) (api.SearchSchedulableRunsOutput, error) {
	p, err := principal(ctx)
	if err != nil {
		return api.SearchSchedulableRunsOutput{}, err
	}
	res, err := s.read.SearchQueue(ctx, p, q, in)
	if err != nil {
		return api.SearchSchedulableRunsOutput{}, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	out := api.SearchSchedulableRunsOutput{
		Edges:    make([]api.RunWithPositionConnection, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for i, e := range res.Edges {
		v := e.Node
		out.Edges[i] = api.RunWithPositionConnection{Cursor: e.Cursor, Node: api.RunWithPosition{
			Position: int32(min(v.Position, math.MaxInt32)), //nolint:gosec // clamped
			Provider: api.VCSProvider(v.Provider),
			Run:      api.RunRef{Id: v.Run.ID.String()},
			Space:    api.SpaceRef{Id: v.Space},
			StackId:  v.Stack.Slug, StackName: v.Stack.Name,
		}}
	}
	return out, nil
}

func (s Server) suggestQueue(ctx context.Context, q read.Queue, in search.SuggestionsInput) (api.SearchSuggestionsOutput, error) {
	p, err := principal(ctx)
	if err != nil {
		return api.SearchSuggestionsOutput{}, err
	}
	got, err := s.read.SuggestQueue(ctx, p, q, in)
	if err != nil {
		return api.SearchSuggestionsOutput{}, err //nolint:wrapcheck // as above
	}
	return apisearch.Suggestions(got), nil
}
