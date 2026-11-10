package http

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Describe is a provider's details of an integration, which only a
// caller with SPACE_ADMIN in its space sees.
type Describe func(in integration.Integration) (api.VCSIntegrationDetails, error)

// Server is the vcs domain's partial of the API's strict server. The
// route table has decided each operation's action on the integration's
// space, or on the space an attachable list names, before its handler
// runs, and answered 404 for one the caller cannot read.
type Server struct {
	svc      *integration.Service
	spaces   accessapi.Spaces
	describe map[string]Describe
}

// New returns the partial over the service, naming spaces through
// spaces, with each provider's details by its served VCSProvider value.
func New(
	svc *integration.Service,
	spaces accessapi.Spaces,
	describe map[string]Describe,
) Server {
	return Server{svc: svc, spaces: spaces, describe: describe}
}

// VcsIntegrations lists the integrations of the caller's account in the
// spaces it may read, by name.
func (s Server) VcsIntegrations(
	ctx context.Context,
	req api.VcsIntegrationsRequestObject,
) (api.VcsIntegrationsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Search(ctx, p, page(req.Params.Cursor, req.Params.Limit))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a bad cursor is a problem
	}
	out, err := s.page(ctx, p, res)
	if err != nil {
		return nil, err
	}
	return api.VcsIntegrations200JSONResponse{Body: out}, nil
}

// AttachableVCSIntegrationsForProviderInSpace lists the integrations of a
// provider that a stack in the space may bind and the caller may read.
func (s Server) AttachableVCSIntegrationsForProviderInSpace(
	ctx context.Context,
	req api.AttachableVCSIntegrationsForProviderInSpaceRequestObject,
) (api.AttachableVCSIntegrationsForProviderInSpaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Attachable(ctx, p, req.Params.Id, string(req.Params.Provider),
		page(req.Params.Cursor, req.Params.Limit))
	if err != nil {
		return nil, integrationError(err)
	}
	out, err := s.page(ctx, p, res)
	if err != nil {
		return nil, err
	}
	return api.AttachableVCSIntegrationsForProviderInSpace200JSONResponse{Body: out}, nil
}

// SearchVCSIntegrations searches the integrations of the caller's account
// in the spaces it may read.
func (s Server) SearchVCSIntegrations(
	ctx context.Context,
	req api.SearchVCSIntegrationsRequestObject,
) (api.SearchVCSIntegrationsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.Search(ctx, p, apisearch.Input(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	out := api.SearchVCSIntegrationsOutput{
		Edges:    make([]api.VCSIntegrationsConnection, 0, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.VCSIntegrationsConnection{
			Cursor: e.Cursor, Node: api.VCSIntegrationRef{Id: e.Node.ID.String()},
		})
	}
	return api.SearchVCSIntegrations200JSONResponse{Body: out}, nil
}

// SearchVCSIntegrationsSuggestions lists the values the integrations of
// the caller's account in the spaces it may read hold.
func (s Server) SearchVCSIntegrationsSuggestions(
	ctx context.Context,
	req api.SearchVCSIntegrationsSuggestionsRequestObject,
) (api.SearchVCSIntegrationsSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.svc.Suggest(ctx, p, apisearch.SuggestionsInput(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return api.SearchVCSIntegrationsSuggestions200JSONResponse{
		Body: apisearch.Suggestions(got),
	}, nil
}

// VcsIntegration answers one integration of the caller's account, with
// its provider's details when the caller administers its space.
func (s Server) VcsIntegration(
	ctx context.Context,
	req api.VcsIntegrationRequestObject,
) (api.VcsIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, admin, err := s.svc.Read(ctx, p, req.VcsIntegrationId, string(req.Params.Provider))
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	v := viewOf(in, slugs)
	if d, ok := s.describe[in.Provider]; ok && admin {
		details, err := d(in)
		if err != nil {
			return nil, err
		}
		v.Details = &details
	}
	return api.VcsIntegration200JSONResponse{Body: v}, nil
}

// VcsIntegrationTest runs the connection test of an integration's
// provider.
func (s Server) VcsIntegrationTest(
	ctx context.Context,
	req api.VcsIntegrationTestRequestObject,
) (api.VcsIntegrationTestResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	checks, err := s.svc.Test(ctx, p, req.VcsIntegrationId, string(req.Params.Provider))
	if err != nil {
		return nil, integrationError(err)
	}
	return api.VcsIntegrationTest200JSONResponse{Body: testResult(checks)}, nil
}

// testResult is checks as the API answers them, failed when any is.
func testResult(checks []port.Check) api.VCSIntegrationTestResult {
	out := api.VCSIntegrationTestResult{
		Checks: make([]api.VCSIntegrationCheck, 0, len(checks)), Status: api.SUCCESS,
	}
	for _, c := range checks {
		check := api.VCSIntegrationCheck{
			Type: api.VCSIntegrationTestType(c.Type), Status: api.SUCCESS,
		}
		if !c.Passed {
			check.Status, out.Status = api.FAIL, api.FAIL
			reason := c.Reason
			check.Metadata = &api.VCSIntegrationTestResultMetadata{
				GithubAppEvents: []string{}, Permissions: []string{}, Reason: &reason,
			}
		}
		out.Checks = append(out.Checks, check)
	}
	return out
}

// viewOf is in as the API answers it to any reader of its space, its
// space named by its slug in slugs.
func viewOf(in integration.Integration, slugs map[kernel.ULID]string) api.VCSIntegration {
	labels := in.Labels
	if labels == nil {
		labels = []string{}
	}
	return api.VCSIntegration{
		Id: in.ID.String(), Name: in.Name, Description: in.Description, Labels: labels,
		IsDefault: in.IsDefault, Provider: api.VCSProvider(in.Provider),
		Space:     api.SpaceRef{Id: slugs[in.Space]},
		CreatedAt: int32(in.CreatedAt.Unix()), //nolint:gosec // the API's seconds are int32
		UpdatedAt: int32(in.UpdatedAt.Unix()), //nolint:gosec // as above
	}
}

// page is a list's cursor and limit as a search's input.
func page(cursor *api.Cursor, limit *api.Limit) search.Input {
	in := search.Input{First: limit}
	if cursor != nil {
		in.After = *cursor
	}
	return in
}

// page is res as a page of views.
func (s Server) page(
	ctx context.Context,
	p kernel.Principal,
	res search.Result[integration.Integration],
) (api.VCSIntegrationPage, error) {
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.VCSIntegrationPage{}, err //nolint:wrapcheck // the access domain names the read
	}
	out := api.VCSIntegrationPage{Items: make([]api.VCSIntegration, 0, len(res.Edges))}
	for _, e := range res.Edges {
		out.Items = append(out.Items, viewOf(e.Node, slugs))
	}
	if res.HasNext {
		out.NextCursor = &res.End
	}
	return out, nil
}

func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return kernel.Principal{}, problem.Unauthenticated.Error(
			"The request carries no valid credential.")
	}
	return p, nil
}

// integrationError answers the service's refusals. A *kernel.Invalid is
// answered as it is.
func integrationError(err error) error {
	if m, ok := errors.AsType[*integration.Missing](err); ok {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No %s %q in this account.", m.Kind, m.ID))
	}
	if errors.Is(err, integration.ErrNotFound) {
		//nolint:wrapcheck // as above
		return problem.NotFound.Error("No such VCS integration in this account.")
	}
	if errors.Is(err, integration.ErrNoTester) {
		//nolint:wrapcheck // as above
		return problem.NotImplemented.Error(
			"infraweave has no connection test for this integration's provider yet.")
	}
	return err
}
