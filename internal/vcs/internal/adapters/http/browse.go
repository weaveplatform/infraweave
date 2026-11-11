package http

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	cursors "github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// The lists a browse cursor may come from.
const (
	repositoriesList = "vcs-repositories"
	branchesList     = "vcs-branches"
)

// Repositories lists the repositories an integration reaches, by
// namespace/name path.
func (s Server) Repositories(
	ctx context.Context,
	req api.RepositoriesRequestObject,
) (api.RepositoriesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.svc.Repositories(ctx, p, string(deref(req.Params.Provider)),
		deref(req.Params.VcsIntegrationID), deref(req.Params.Filter))
	if err != nil {
		return nil, browseError(err)
	}
	items, next, err := pageOf(repositoriesList, all,
		func(l port.Listed) string { return l.Namespace + "/" + l.Name },
		req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	out := api.RepositoryPage{Items: make([]api.Repository, 0, len(items)), NextCursor: next}
	for _, l := range items {
		out.Items = append(out.Items, api.Repository{Id: l.Name, Namespace: l.Namespace})
	}
	return api.Repositories200JSONResponse{Body: out}, nil
}

// Branches lists a repository's branches, by name.
func (s Server) Branches(
	ctx context.Context,
	req api.BranchesRequestObject,
) (api.BranchesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.svc.Branches(ctx, p, string(req.Params.Provider),
		deref(req.Params.VcsIntegrationID), port.Repository{
			Namespace: req.Params.Namespace, Name: req.Params.Repository,
			URL: deref(req.Params.RepositoryURL),
		})
	if err != nil {
		return nil, browseError(err)
	}
	items, next, err := pageOf(branchesList, all,
		func(b string) string { return b }, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	return api.Branches200JSONResponse{Body: api.BranchesPage{Items: items, NextCursor: next}}, nil
}

// VcsIntegrationSearchUsers searches the stacks that use an integration.
func (s Server) VcsIntegrationSearchUsers(
	ctx context.Context,
	req api.VcsIntegrationSearchUsersRequestObject,
) (api.VcsIntegrationSearchUsersResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.SearchUsers(ctx, p, req.VcsIntegrationId, apisearch.Input(req.Params.Input))
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	out := api.SearchVCSIntegrationUsersOutput{
		Edges:    make([]api.VCSIntegrationUserConnection, 0, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.VCSIntegrationUserConnection{
			Cursor: e.Cursor,
			Node: api.VCSIntegrationUser{
				StackId: e.Node.Slug, StackName: e.Node.Name,
				SpaceDetails: api.SpaceRef{Id: slugs[e.Node.Space]},
			},
		})
	}
	return api.VcsIntegrationSearchUsers200JSONResponse{Body: out}, nil
}

// VcsIntegrationSearchUsersSuggestions lists the values the stacks that
// use an integration hold.
func (s Server) VcsIntegrationSearchUsersSuggestions(
	ctx context.Context,
	req api.VcsIntegrationSearchUsersSuggestionsRequestObject,
) (api.VcsIntegrationSearchUsersSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.svc.SuggestUsers(ctx, p, req.VcsIntegrationId,
		apisearch.SuggestionsInput(req.Params.Input))
	if err != nil {
		return nil, integrationError(err)
	}
	return api.VcsIntegrationSearchUsersSuggestions200JSONResponse{
		Body: apisearch.Suggestions(got),
	}, nil
}

// pageOf is the page of all, sorted by key, after cursor. A provider
// answers its whole list in one read, so the page is cut here; the cursor
// holds the last key served, which stays a position while the list
// changes between pages.
func pageOf[T any](
	list string,
	all []T,
	key func(T) string,
	cursor *api.Cursor,
	limit *api.Limit,
) ([]T, *string, error) {
	slices.SortFunc(all, func(a, b T) int { return strings.Compare(key(a), key(b)) })
	if cursor != nil {
		c, err := cursors.Decode[string](list, *cursor)
		if err != nil {
			return nil, nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		all = slices.DeleteFunc(all, func(t T) bool { return key(t) <= c.Key })
	}
	n := int(kernel.PageLimit(limit))
	if len(all) <= n {
		return all, nil, nil
	}
	all = all[:n]
	last := key(all[n-1])
	next, err := cursors.Encode(list, kernel.Cursor[string]{Key: last, ID: last})
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // encoding a string cannot fail
	}
	return all, &next, nil
}

// browseError is err as the problem a browse answers: a repository the
// provider lacks is not found, and a credential it refuses is the
// upstream's error, not the caller's.
func browseError(err error) error {
	if errors.Is(err, port.ErrNotFound) {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error("The provider has no such repository.")
	}
	if errors.Is(err, port.ErrCredential) {
		//nolint:wrapcheck // as above
		return problem.UpstreamError.Error("The provider refuses the integration's credential.")
	}
	return integrationError(err)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
