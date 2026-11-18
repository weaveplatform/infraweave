package http_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// limited is a tester its host refuses for its rate limit.
type limited struct{}

func (limited) Test(context.Context, port.Integration) ([]port.Check, error) {
	return nil, &port.RateLimited{Reset: time.Unix(1_700_000_000, 0)}
}

// gitHubFixture serves an account-owned App whose tester is rate limited.
type gitHubFixture struct {
	srv    http.Server
	store  *store
	spaces *spaces
	ctx    context.Context
	ghe    string
}

func newGitHubFixture(t *testing.T) gitHubFixture {
	t.Helper()
	acct := kernel.NewAccountID()
	ghe := kernel.NewULID()
	st := &store{rows: map[kernel.ULID]integration.Integration{
		ghe: {
			Account: acct, ID: ghe, Provider: integration.GitHubEnterprise, Name: "Enterprise",
			Space: acct.ULID, Settings: port.Settings{APIHost: "https://ghe.example", AppID: "7"},
		},
	}}
	sp := &spaces{top: acct.ULID}
	d := integration.Deps{
		Store: st, Spaces: sp, Authz: authz{top: acct.ULID},
		Providers: []string{integration.GitHubEnterprise},
		Testers:   map[string]port.Tester{integration.GitHubEnterprise: limited{}},
		Now:       time.Now,
	}
	return gitHubFixture{
		srv: http.New(integration.New(d), sp, nil), store: st, spaces: sp,
		ctx: kernel.WithPrincipal(t.Context(), kernel.Principal{AccountID: acct}),
		ghe: ghe.String(),
	}
}

// calls are the GitHub operations, each answering only its error.
func (f gitHubFixture) calls(ctx context.Context) map[string]func() error {
	s, id := f.srv, f.ghe
	return map[string]func() error{
		"create": func() error {
			_, err := s.GithubEnterpriseIntegrationCreate(ctx,
				api.GithubEnterpriseIntegrationCreateRequestObject{
					Body: &api.GithubEnterpriseIntegrationCreateJSONRequestBody{},
				})
			return err
		},
		"read": func() error {
			_, err := s.GithubEnterpriseIntegration(ctx,
				api.GithubEnterpriseIntegrationRequestObject{GithubEnterpriseIntegrationId: id})
			return err
		},
		"update": func() error {
			_, err := s.GithubEnterpriseIntegrationUpdate(ctx,
				api.GithubEnterpriseIntegrationUpdateRequestObject{
					GithubEnterpriseIntegrationId: id,
					Body:                          &api.GithubEnterpriseIntegrationUpdateJSONRequestBody{},
				})
			return err
		},
		"patch": func() error {
			_, err := s.GithubEnterpriseIntegrationPatch(ctx, api.GithubEnterpriseIntegrationPatchRequestObject{GithubEnterpriseIntegrationId: id})
			return err
		},
		"delete": func() error {
			_, err := s.GithubEnterpriseIntegrationDelete(
				ctx,
				api.GithubEnterpriseIntegrationDeleteRequestObject{
					GithubEnterpriseIntegrationId: id,
				},
			)
			return err
		},
		"test": func() error {
			_, err := s.GithubEnterpriseIntegrationTest(ctx,
				api.GithubEnterpriseIntegrationTestRequestObject{GithubEnterpriseIntegrationId: id})
			return err
		},
		"manifest": func() error {
			_, err := s.GithubAppGenerateManifest(ctx, api.GithubAppGenerateManifestRequestObject{
				Body: &api.GithubAppGenerateManifestJSONRequestBody{OwnerType: api.AccountTypeUSER},
			})
			return err
		},
		"from manifest": func() error {
			_, err := s.GithubAppCreateFromManifest(
				ctx,
				api.GithubAppCreateFromManifestRequestObject{
					Body: &api.GithubAppCreateFromManifestJSONRequestBody{Code: "c", State: "s"},
				},
			)
			return err
		},
	}
}

// TestUnitGitHub checks account-owned App reads, alias reads, upstream
// failures and manifest work without an App client.
func TestUnitGitHub(t *testing.T) {
	f := newGitHubFixture(t)
	for _, provider := range []api.VCSProvider{api.VCSProviderGITHUB, api.VCSProviderGITHUBENTERPRISE} {
		read, err := f.srv.VcsIntegration(f.ctx, api.VcsIntegrationRequestObject{
			VcsIntegrationId: f.ghe, Params: api.VcsIntegrationParams{Provider: provider},
		})
		if err != nil || read.(api.VcsIntegration200JSONResponse).Body.Details == nil {
			t.Errorf("details of %s = %+v, %v", provider, read, err)
		}
	}
	if err := f.calls(f.ctx)["test"](); !is(err, problem.UpstreamError) {
		t.Errorf("a rate-limited test = %v", err)
	}
	for name, call := range f.calls(t.Context()) {
		if err := call(); !is(err, problem.Unauthenticated) {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}
	for _, name := range []string{"manifest", "from manifest"} {
		if err := f.calls(f.ctx)[name](); !is(err, problem.ServiceUnavailable) {
			t.Errorf("%s with no App client = %v", name, err)
		}
	}
	f.spaces.fail = errBoom
	if err := f.calls(f.ctx)["read"](); !errors.Is(err, errBoom) {
		t.Errorf("read with failing slugs = %v", err)
	}
	f.spaces.fail = nil
	f.store.fail = errBoom
	for _, name := range []string{"read", "update", "patch", "test"} {
		if err := f.calls(f.ctx)[name](); !errors.Is(err, errBoom) {
			t.Errorf("%s on a failing store = %v", name, err)
		}
	}
}
