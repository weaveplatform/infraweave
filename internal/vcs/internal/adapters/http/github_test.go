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

// gitHubFixture is a server over the operator's App, with check options
// stored, and a GitHub Enterprise integration whose tester is rate
// limited; builtin false leaves the operator's App unconfigured.
type gitHubFixture struct {
	srv    http.Server
	store  *store
	spaces *spaces
	ctx    context.Context
	ghe    string
}

func newGitHubFixture(t *testing.T, builtin bool) gitHubFixture {
	t.Helper()
	acct := kernel.NewAccountID()
	op, ghe := kernel.NewULID(), kernel.NewULID()
	st := &store{rows: map[kernel.ULID]integration.Integration{
		op: {
			Account: acct, ID: op, Provider: integration.GitHub, Space: acct.ULID,
			IsDefault: true, Checks: integration.Checks{Mode: "ALL"},
		},
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
	if builtin {
		d.Builtin = map[string]port.Integration{integration.GitHub: {
			Provider: integration.GitHub,
			Settings: port.Settings{APIHost: "https://api.github.com"},
		}}
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
	yes := true
	return map[string]func() error{
		"github": func() error {
			_, err := s.GithubIntegration(ctx, api.GithubIntegrationRequestObject{})
			return err
		},
		"github update": func() error {
			_, err := s.GithubIntegrationUpdate(ctx, api.GithubIntegrationUpdateRequestObject{
				Body: &api.GithubIntegrationUpdateJSONRequestBody{
					VcsChecks: api.ALL, UseGitCheckout: &yes,
					VcsCheckOptions: &api.VCSCheckOptionsInput{
						IndividualFailuresOnly: &yes, IndividualSkipUnchanged: &yes,
					},
				},
			})
			return err
		},
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

// TestUnitGitHub checks that the operator's App answers its stored check
// options, and its details to an admin of the integration's space as a
// GitHub Enterprise integration's are; that a rate-limited test answers
// upstream-error; that no principal is unauthenticated; that an
// unconfigured operator's App and manifest flow answer
// service-unavailable; and that failing slugs and a failing store answer
// their error.
func TestUnitGitHub(t *testing.T) {
	f := newGitHubFixture(t, true)
	got, err := f.srv.GithubIntegration(f.ctx, api.GithubIntegrationRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if v := got.(api.GithubIntegration200JSONResponse).Body; v.VcsChecks != api.ALL ||
		v.IsEnterprise {
		t.Errorf("github = %+v", v)
	}
	for _, provider := range []api.VCSProvider{api.VCSProviderGITHUB, api.VCSProviderGITHUBENTERPRISE} {
		var id string
		for k, in := range f.store.rows {
			if in.Provider == string(provider) {
				id = k.String()
			}
		}
		read, err := f.srv.VcsIntegration(f.ctx, api.VcsIntegrationRequestObject{
			VcsIntegrationId: id, Params: api.VcsIntegrationParams{Provider: provider},
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

	bare := newGitHubFixture(t, false)
	for _, name := range []string{"github", "github update", "manifest", "from manifest"} {
		if err := bare.calls(bare.ctx)[name](); !is(err, problem.ServiceUnavailable) {
			t.Errorf("%s with no operator App = %v", name, err)
		}
	}
	for k, in := range bare.store.rows {
		if in.Provider == integration.GitHub {
			_, err := bare.srv.VcsIntegration(bare.ctx, api.VcsIntegrationRequestObject{
				VcsIntegrationId: k.String(),
				Params:           api.VcsIntegrationParams{Provider: api.VCSProviderGITHUB},
			})
			if !is(err, problem.ServiceUnavailable) {
				t.Errorf("details of an unconfigured operator's App = %v", err)
			}
		}
	}

	f.spaces.fail = errBoom
	if err := f.calls(f.ctx)["read"](); !errors.Is(err, errBoom) {
		t.Errorf("read with failing slugs = %v", err)
	}
	f.spaces.fail = nil
	f.store.fail = errBoom
	for _, name := range []string{"github", "github update", "read", "update", "test"} {
		if err := f.calls(f.ctx)[name](); !errors.Is(err, errBoom) {
			t.Errorf("%s on a failing store = %v", name, err)
		}
	}
}
