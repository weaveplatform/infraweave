package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// WorkerPoolSearchUsers searches the stacks that select a private pool,
// in the spaces the caller may read. The route has decided SPACE_READ on
// the pool's space, and answered 404 for a pool the caller cannot read.
func (s Server) WorkerPoolSearchUsers(ctx context.Context, req api.WorkerPoolSearchUsersRequestObject) (api.WorkerPoolSearchUsersResponseObject, error) {
	out, err := s.searchUsers(ctx, poolUsers(req.WorkerPoolId), apisearch.Input(req.Params.Input))
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolSearchUsers200JSONResponse{Body: out}, nil
}

// WorkerPoolSearchUsersSuggestions lists the values the stacks that
// select a private pool hold.
func (s Server) WorkerPoolSearchUsersSuggestions(
	ctx context.Context,
	req api.WorkerPoolSearchUsersSuggestionsRequestObject,
) (api.WorkerPoolSearchUsersSuggestionsResponseObject, error) {
	out, err := s.suggestUsers(ctx, poolUsers(req.WorkerPoolId), apisearch.SuggestionsInput(req.Params.Input))
	if err != nil {
		return nil, err
	}
	return api.WorkerPoolSearchUsersSuggestions200JSONResponse{Body: out}, nil
}

// PublicWorkerPoolSearchUsers searches the caller's account's stacks
// that select the public pool, in the spaces the caller may read.
func (s Server) PublicWorkerPoolSearchUsers(ctx context.Context, req api.PublicWorkerPoolSearchUsersRequestObject) (api.PublicWorkerPoolSearchUsersResponseObject, error) {
	out, err := s.searchUsers(ctx, core.Users{}, apisearch.Input(req.Params.Input))
	if err != nil {
		return nil, err
	}
	return api.PublicWorkerPoolSearchUsers200JSONResponse{Body: out}, nil
}

// PublicWorkerPoolSearchUsersSuggestions lists the values the stacks that
// select the public pool hold.
func (s Server) PublicWorkerPoolSearchUsersSuggestions(
	ctx context.Context,
	req api.PublicWorkerPoolSearchUsersSuggestionsRequestObject,
) (api.PublicWorkerPoolSearchUsersSuggestionsResponseObject, error) {
	out, err := s.suggestUsers(ctx, core.Users{}, apisearch.SuggestionsInput(req.Params.Input))
	if err != nil {
		return nil, err
	}
	return api.PublicWorkerPoolSearchUsersSuggestions200JSONResponse{Body: out}, nil
}

// poolUsers names the users of the pool id names. The route resolved id
// to a pool of the caller's account, so it parses.
func poolUsers(id string) core.Users {
	pool, _ := kernel.ParseULID(id)
	return core.Users{Pool: &pool}
}

func (s Server) searchUsers(ctx context.Context, u core.Users, in search.Input) (api.SearchWorkerPoolUsersOutput, error) {
	p, err := principal(ctx)
	if err != nil {
		return api.SearchWorkerPoolUsersOutput{}, err
	}
	res, err := s.stacks.SearchPoolUsers(ctx, p, u, in)
	if err != nil {
		return api.SearchWorkerPoolUsersOutput{}, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.SearchWorkerPoolUsersOutput{}, err //nolint:wrapcheck // the access domain names the read
	}
	out := api.SearchWorkerPoolUsersOutput{
		Edges:    make([]api.WorkerPoolUserConnection, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for i, e := range res.Edges {
		out.Edges[i] = api.WorkerPoolUserConnection{Cursor: e.Cursor, Node: api.WorkerPoolUser{
			StackId: e.Node.Slug, StackName: e.Node.Name,
			SpaceDetails: api.SpaceRef{Id: slugs[e.Node.Space]},
		}}
	}
	return out, nil
}

func (s Server) suggestUsers(ctx context.Context, u core.Users, in search.SuggestionsInput) (api.SearchSuggestionsOutput, error) {
	p, err := principal(ctx)
	if err != nil {
		return api.SearchSuggestionsOutput{}, err
	}
	got, err := s.stacks.SuggestPoolUsers(ctx, p, u, in)
	if err != nil {
		return api.SearchSuggestionsOutput{}, err //nolint:wrapcheck // as above
	}
	return apisearch.Suggestions(got), nil
}
