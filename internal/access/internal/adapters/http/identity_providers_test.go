package http

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

func TestUnitIdentityProvidersNoPrincipal(t *testing.T) {
	ctx := context.Background()
	s := Server{}
	_, e1 := s.OrganizationIdentityProviders(ctx, api.OrganizationIdentityProvidersRequestObject{})
	_, e2 := s.OrganizationIdentityProviderCreate(ctx, api.OrganizationIdentityProviderCreateRequestObject{})
	_, e3 := s.OrganizationIdentityProviderUpdate(ctx, api.OrganizationIdentityProviderUpdateRequestObject{})
	_, e4 := s.OrganizationIdentityProviderDelete(ctx, api.OrganizationIdentityProviderDeleteRequestObject{})
	_, e5 := s.OrganizationIdentityProviderSetDefault(ctx, api.OrganizationIdentityProviderSetDefaultRequestObject{})
	for i, err := range []error{e1, e2, e3, e4, e5} {
		if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != http.StatusUnauthorized {
			t.Errorf("handler %d without a principal = %v", i, err)
		}
	}
}

func TestUnitProviderError(t *testing.T) {
	for _, c := range []struct {
		name          string
		err           error
		id            string
		status        int
		slug, feature string
		tiers         []string
	}{
		{
			name: "not in tier", err: &featuresapi.Refusal{Feature: "saml-sso", Tiers: []string{"Enterprise"}},
			status: http.StatusForbidden, slug: problem.FeatureNotInTier.Slug, feature: "saml-sso",
			tiers: []string{"Enterprise"},
		},
		{
			name: "switched off", err: &featuresapi.Refusal{Disabled: true, Feature: "oidc-sso"},
			status: http.StatusForbidden, slug: problem.FeatureDisabled.Slug, feature: "oidc-sso",
		},
		{
			name: "forbidden", err: orgadmin.ErrForbidden,
			status: http.StatusForbidden, slug: problem.Forbidden.Slug,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			pe, ok := errors.AsType[*problem.Error](providerError(c.err, c.id))
			if !ok {
				t.Fatalf("providerError = %v, want a problem", c.err)
			}
			got := pe.Problem
			if got.Status != c.status || got.Type != problem.TypePrefix+c.slug || got.Feature != c.feature ||
				!cmp.Equal(got.Tiers, c.tiers) {
				t.Errorf("problem = %+v", got)
			}
		})
	}
}

func TestUnitProviderView(t *testing.T) {
	created := time.Unix(1_700_000_000, 0)
	p := sso.Provider{
		Kind: sso.GitHubOAuth, Name: "github", Default: true, EnabledAt: created, AdminLogin: "octocat",
		CreatedAt: created,
		Settings:  sso.Settings{BaseURL: sso.GitHubURL, ClientID: "Iv1.acme", Organization: "acme"},
	}
	got := providerView(p)
	if !got.Enabled || got.AdminLogin == nil || *got.AdminLogin != "octocat" || got.CreatedAt != created.Unix() {
		t.Errorf("view = %+v", got)
	}
	if got.Settings.ClaimMapping != nil || got.Settings.IdentityProviderHost != nil {
		t.Errorf("GitHub OAuth settings = %+v, want no OIDC or SAML setting", got.Settings)
	}
	if v := providerView(sso.Provider{Kind: sso.OIDC}); v.Enabled || v.AdminLogin != nil {
		t.Errorf("disabled view = %+v", v)
	}
}
