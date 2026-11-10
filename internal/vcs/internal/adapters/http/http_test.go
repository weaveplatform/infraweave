package http_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var errBoom = errors.New("boom")

// store holds integrations by ID, failing every read when fail is set.
type store struct {
	integration.Store

	rows map[kernel.ULID]integration.Integration
	fail error
}

func (s *store) Get(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
	_ bool,
) (integration.Integration, error) {
	if s.fail != nil {
		return integration.Integration{}, s.fail
	}
	in, ok := s.rows[id]
	if !ok {
		return in, integration.ErrNotFound
	}
	return in, nil
}

func (s *store) Secrets(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) (integration.Secrets, error) {
	return integration.Secrets{Credential: []byte("token")}, nil
}

func (s *store) Search(
	context.Context,
	kernel.AccountID,
	search.Input,
	integration.Scope,
) (search.Result[integration.Integration], error) {
	if s.fail != nil {
		return search.Result[integration.Integration]{}, s.fail
	}
	out := search.Result[integration.Integration]{HasNext: true, End: "end"}
	for _, in := range s.rows {
		out.Edges = append(out.Edges, search.Edge[integration.Integration]{Cursor: "c", Node: in})
	}
	return out, nil
}

func (s *store) Suggest(
	context.Context,
	kernel.AccountID,
	search.SuggestionsInput,
	integration.Scope,
) (search.Suggestions, error) {
	return search.Suggestions{}, s.fail
}

// spaces is one account's top space, failing Slugs when fail is set.
type spaces struct {
	accessapi.Spaces

	top  kernel.ULID
	fail error
}

func (s *spaces) Find(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
	if slug != "acme" {
		return kernel.ULID{}, accessapi.ErrNoSpace
	}
	return s.top, nil
}

func (s *spaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	return map[kernel.ULID]string{s.top: "acme"}, s.fail
}

func (s *spaces) Tree(context.Context, kernel.AccountID) ([]accessapi.SpaceNode, error) {
	return []accessapi.SpaceNode{{ID: s.top}}, nil
}

// authz lets the principal do anything in the top space.
type authz struct {
	accessapi.Authorizer

	top kernel.ULID
}

func (a authz) Authorize(
	context.Context,
	kernel.Principal,
	accessapi.Action,
	kernel.ULID,
) (bool, error) {
	return true, nil
}

func (a authz) Reachable(
	context.Context,
	kernel.Principal,
	accessapi.Action,
) ([]kernel.ULID, error) {
	return []kernel.ULID{a.top}, nil
}

// tester passes the connection check and fails the permissions one.
type tester struct{}

func (tester) Test(context.Context, port.Integration) ([]port.Check, error) {
	return []port.Check{
		{Type: port.CheckConnection, Passed: true},
		{Type: port.CheckPermissions, Reason: "no read access"},
	}, nil
}

// fixture is a server over a GITLAB integration with a tester and a
// BITBUCKET_CLOUD one without, whose details fail to describe.
type fixture struct {
	srv           http.Server
	store         *store
	spaces        *spaces
	ctx           context.Context
	gitlab, cloud string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	acct := kernel.NewAccountID()
	gitlab, cloud := kernel.NewULID(), kernel.NewULID()
	st := &store{rows: map[kernel.ULID]integration.Integration{
		gitlab: {Account: acct, ID: gitlab, Provider: "GITLAB", Name: "Main", Space: acct.ULID},
		cloud: {
			Account: acct, ID: cloud, Provider: "BITBUCKET_CLOUD", Name: "Cloud",
			Space: acct.ULID, Labels: []string{"infra"},
		},
	}}
	sp := &spaces{top: acct.ULID}
	svc := integration.New(integration.Deps{
		Store: st, Spaces: sp, Authz: authz{top: acct.ULID},
		Providers: []string{"GITLAB", "BITBUCKET_CLOUD"},
		Testers:   map[string]port.Tester{"GITLAB": tester{}},
		Now:       time.Now,
	})
	srv := http.New(svc, sp, map[string]http.Describe{
		"GITLAB": func(integration.Integration) (api.VCSIntegrationDetails, error) {
			return api.VCSIntegrationDetails{}, nil
		},
		"BITBUCKET_CLOUD": func(integration.Integration) (api.VCSIntegrationDetails, error) {
			return api.VCSIntegrationDetails{}, errBoom
		},
	})
	ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{AccountID: acct})
	return fixture{
		srv: srv, store: st, spaces: sp, ctx: ctx, gitlab: gitlab.String(), cloud: cloud.String(),
	}
}

// is reports whether err is a problem of type t.
func is(err error, t problem.Type) bool {
	pe, ok := errors.AsType[*problem.Error](err)
	return ok && pe.Problem.Title == t.Title && pe.Problem.Status == t.Status
}

// calls are the server's operations, each answering only its error.
func (f fixture) calls(ctx context.Context) map[string]func() error {
	s := f.srv
	return map[string]func() error{
		"list": func() error {
			_, err := s.VcsIntegrations(ctx, api.VcsIntegrationsRequestObject{})
			return err
		},
		"attachable": func() error {
			_, err := s.AttachableVCSIntegrationsForProviderInSpace(ctx,
				api.AttachableVCSIntegrationsForProviderInSpaceRequestObject{
					Params: api.AttachableVCSIntegrationsForProviderInSpaceParams{
						Id: "acme", Provider: api.VCSProviderGITLAB,
					},
				})
			return err
		},
		"search": func() error {
			_, err := s.SearchVCSIntegrations(ctx, api.SearchVCSIntegrationsRequestObject{
				Body: &api.SearchVCSIntegrationsJSONRequestBody{},
			})
			return err
		},
		"suggest": func() error {
			_, err := s.SearchVCSIntegrationsSuggestions(ctx,
				api.SearchVCSIntegrationsSuggestionsRequestObject{
					Body: &api.SearchVCSIntegrationsSuggestionsJSONRequestBody{},
				})
			return err
		},
		"read": func() error {
			_, err := s.VcsIntegration(ctx, api.VcsIntegrationRequestObject{
				VcsIntegrationId: f.gitlab,
				Params:           api.VcsIntegrationParams{Provider: api.VCSProviderGITLAB},
			})
			return err
		},
		"test": func() error {
			_, err := s.VcsIntegrationTest(ctx, api.VcsIntegrationTestRequestObject{
				VcsIntegrationId: f.gitlab,
				Params:           api.VcsIntegrationTestParams{Provider: api.VCSProviderGITLAB},
			})
			return err
		},
	}
}

// TestUnitAnswers checks that each operation answers its integrations,
// the details of each to an admin, and a test's failed check as a failed
// result.
func TestUnitAnswers(t *testing.T) {
	f := newFixture(t)
	for name, call := range f.calls(f.ctx) {
		if err := call(); err != nil {
			t.Errorf("%s = %v", name, err)
		}
	}

	got, err := f.srv.VcsIntegrations(f.ctx, api.VcsIntegrationsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	page := got.(api.VcsIntegrations200JSONResponse).Body
	if len(page.Items) != 2 || page.NextCursor == nil || *page.NextCursor != "end" {
		t.Errorf("list = %+v", page)
	}
	for _, in := range page.Items {
		if in.Labels == nil || in.Space.Id != "acme" {
			t.Errorf("view %+v carries no labels or no space slug", in)
		}
	}

	read, err := f.srv.VcsIntegration(f.ctx, api.VcsIntegrationRequestObject{
		VcsIntegrationId: f.gitlab,
		Params:           api.VcsIntegrationParams{Provider: api.VCSProviderGITLAB},
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.(api.VcsIntegration200JSONResponse).Body.Details == nil {
		t.Error("an admin's read carries no details")
	}

	res, err := f.srv.VcsIntegrationTest(f.ctx, api.VcsIntegrationTestRequestObject{
		VcsIntegrationId: f.gitlab,
		Params:           api.VcsIntegrationTestParams{Provider: api.VCSProviderGITLAB},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := res.(api.VcsIntegrationTest200JSONResponse).Body
	if body.Status != api.FAIL || len(body.Checks) != 2 || body.Checks[0].Status != api.SUCCESS ||
		body.Checks[1].Metadata == nil || *body.Checks[1].Metadata.Reason != "no read access" {
		t.Errorf("test = %+v", body)
	}
}

// TestUnitRefusals checks the problems each refusal answers: no
// principal, a space the account lacks, a provider with no test, details
// that fail to describe, a failing store and failing space slugs.
func TestUnitRefusals(t *testing.T) {
	f := newFixture(t)
	for name, call := range f.calls(t.Context()) {
		if err := call(); !is(err, problem.Unauthenticated) {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}

	_, err := f.srv.AttachableVCSIntegrationsForProviderInSpace(f.ctx,
		api.AttachableVCSIntegrationsForProviderInSpaceRequestObject{
			Params: api.AttachableVCSIntegrationsForProviderInSpaceParams{
				Id: "nowhere", Provider: api.VCSProviderGITLAB,
			},
		})
	if !is(err, problem.NotFound) {
		t.Errorf("attachable in a missing space = %v", err)
	}
	_, err = f.srv.VcsIntegrationTest(f.ctx, api.VcsIntegrationTestRequestObject{
		VcsIntegrationId: f.cloud,
		Params:           api.VcsIntegrationTestParams{Provider: api.VCSProviderBITBUCKETCLOUD},
	})
	if !is(err, problem.NotImplemented) {
		t.Errorf("test of a provider with no tester = %v", err)
	}
	_, err = f.srv.VcsIntegration(f.ctx, api.VcsIntegrationRequestObject{
		VcsIntegrationId: f.cloud,
		Params:           api.VcsIntegrationParams{Provider: api.VCSProviderBITBUCKETCLOUD},
	})
	if !errors.Is(err, errBoom) {
		t.Errorf("read whose details fail = %v", err)
	}

	f.spaces.fail = errBoom
	for _, name := range []string{"list", "attachable", "read"} {
		if err := f.calls(f.ctx)[name](); !errors.Is(err, errBoom) {
			t.Errorf("%s with failing slugs = %v", name, err)
		}
	}
	f.spaces.fail = nil
	f.store.fail = errBoom
	for name, call := range f.calls(f.ctx) {
		if err := call(); !errors.Is(err, errBoom) {
			t.Errorf("%s on a failing store = %v", name, err)
		}
	}
}
