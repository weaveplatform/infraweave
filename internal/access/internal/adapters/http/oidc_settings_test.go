package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

func TestUnitOIDCSettingsNoPrincipal(t *testing.T) {
	ctx := context.Background()
	s := Server{}
	_, e1 := s.OidcSettings(ctx, api.OidcSettingsRequestObject{})
	_, e2 := s.OidcCreate(ctx, api.OidcCreateRequestObject{})
	_, e3 := s.OidcUpdate(ctx, api.OidcUpdateRequestObject{})
	_, e4 := s.OidcDelete(ctx, api.OidcDeleteRequestObject{})
	_, e5 := s.HasSSO(ctx, api.HasSSORequestObject{})
	for i, err := range []error{e1, e2, e3, e4, e5} {
		if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != http.StatusUnauthorized {
			t.Errorf("handler %d without a principal = %v", i, err)
		}
	}
}

// TestUnitSSOError answers each refusal of the SSO tag's service as its
// problem, and passes an unreachable store's error through.
func TestUnitSSOError(t *testing.T) {
	down := errors.New("down")
	for _, c := range []struct {
		name   string
		err    error
		status int
		slug   string
		action string
	}{
		{"forbidden", orgadmin.ErrForbidden, http.StatusForbidden, problem.Forbidden.Slug, "ORGANIZATION_ADMIN"},
		{"none", fmt.Errorf("%w: OIDC provider", port.ErrNotFound), http.StatusNotFound, problem.NotFound.Slug, ""},
		{"a second", sso.ErrDefaultExists, http.StatusConflict, problem.AlreadyExists.Slug, ""},
		{
			"invalid", &organization.FieldError{Field: "identityProviderHost", Reason: "is not an https URL"},
			http.StatusUnprocessableEntity, problem.ValidationFailed.Slug, "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			pe, ok := errors.AsType[*problem.Error](ssoError(c.err))
			if !ok {
				t.Fatalf("ssoError(%v) is no problem", c.err)
			}
			if p := pe.Problem; p.Status != c.status || p.Type != problem.TypePrefix+c.slug || p.Action != c.action {
				t.Errorf("problem = %+v", p)
			}
		})
	}
	if err := ssoError(down); !errors.Is(err, down) {
		t.Errorf("an unreachable store = %v", err)
	}
}

// TestUnitOIDCSettingsView answers an OIDC provider's settings with an
// empty claim mapping for none, its admin login null until it is enabled,
// and the claim mapping's entries as the request named them.
func TestUnitOIDCSettingsView(t *testing.T) {
	p := sso.Provider{Kind: sso.OIDC, Settings: sso.Settings{
		IdentityProviderHost: "https://login.example.com", ClientID: "web",
	}}
	got := oidcSettingsView(p)
	want := api.OIDCSettings{
		IdentityProviderHost: "https://login.example.com", ClientId: "web",
		ClaimMapping: api.ClaimMapping{Entries: []api.ClaimMappingEntry{}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("a disabled provider (-want +got):\n%s", diff)
	}
	p.AdminLogin = "ada"
	p.Settings.ClaimMapping = claimMappingInput(&api.ClaimMappingInput{
		Entries: []api.ClaimMappingEntryInput{{Name: "groups", Value: "roles"}},
	})
	got = oidcSettingsView(p)
	if got.AdminLogin == nil || *got.AdminLogin != "ada" ||
		!cmp.Equal(got.ClaimMapping.Entries, []api.ClaimMappingEntry{{Name: "groups", Value: "roles"}}) {
		t.Errorf("an enabled provider = %+v", got)
	}
	if claimMappingInput(nil) != nil {
		t.Error("no claim mapping is not nil")
	}
}
