package http

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// OidcSettings answers the settings of the organization's default OIDC
// provider.
func (s Server) OidcSettings(ctx context.Context, _ api.OidcSettingsRequestObject) (api.OidcSettingsResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.providers.OIDCSettings(ctx, by)
	if err != nil {
		return nil, ssoError(err)
	}
	return api.OidcSettings200JSONResponse{Body: oidcSettingsView(p)}, nil
}

// OidcCreate creates the organization's default OIDC provider, disabled.
func (s Server) OidcCreate(ctx context.Context, req api.OidcCreateRequestObject) (api.OidcCreateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.providers.CreateOIDC(ctx, by, sso.OIDCInput{
		IdentityProviderHost: req.Body.IdentityProviderHost, ClientID: req.Body.ClientId,
		ClientCredentials: req.Body.ClientCredentials, ClaimMapping: claimMappingInput(req.Body.ClaimMapping),
	})
	if err != nil {
		return nil, ssoError(err)
	}
	return api.OidcCreate201JSONResponse{Body: oidcSettingsView(p)}, nil
}

// OidcUpdate replaces the claim mapping of the organization's default OIDC
// provider.
func (s Server) OidcUpdate(ctx context.Context, req api.OidcUpdateRequestObject) (api.OidcUpdateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.providers.UpdateOIDC(ctx, by, claimMappingInput(req.Body.ClaimMapping))
	if err != nil {
		return nil, ssoError(err)
	}
	return api.OidcUpdate200JSONResponse{Body: oidcSettingsView(p)}, nil
}

// OidcDelete deletes the organization's default OIDC provider and answers
// its settings as they were.
func (s Server) OidcDelete(ctx context.Context, _ api.OidcDeleteRequestObject) (api.OidcDeleteResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.providers.DeleteOIDC(ctx, by)
	if err != nil {
		return nil, ssoError(err)
	}
	return api.OidcDelete200JSONResponse{Body: oidcSettingsView(p)}, nil
}

// HasSSO answers whether the organization has an enabled OIDC or SAML
// provider.
//
//nolint:revive // the generated interface names it
func (s Server) HasSSO(ctx context.Context, _ api.HasSSORequestObject) (api.HasSSOResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	on, err := s.providers.HasSSO(ctx, by)
	if err != nil {
		return nil, ssoError(err)
	}
	return api.HasSSO200JSONResponse{Body: on}, nil
}

// ssoError is err as the SSO tag answers it: 403 naming
// ORGANIZATION_ADMIN for a member without the organization role, 404 for
// an organization without a default OIDC provider, 409 already-exists for
// a second one, and otherwise as the Organization tag's identity provider
// operations answer it.
func ssoError(err error) error {
	switch {
	case errors.Is(err, orgadmin.ErrForbidden):
		p := problem.Forbidden.New(fmt.Sprintf(
			"Single sign-on settings need %s, which only the organization role organization-admin holds.",
			accessapi.ActionOrganizationAdmin,
		))
		p.Action = string(accessapi.ActionOrganizationAdmin)
		return &problem.Error{Problem: p}
	case errors.Is(err, port.ErrNotFound):
		return problem.NotFound.Error("The organization has no OIDC settings.") //nolint:wrapcheck // a problem, answered as it is
	case errors.Is(err, sso.ErrDefaultExists):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.AlreadyExists.Error(
			"The organization has OIDC settings; delete them to configure another provider.")
	}
	return providerError(err, "")
}

// claimMappingInput is in's entries, nil for none.
func claimMappingInput(in *api.ClaimMappingInput) []sso.ClaimMappingEntry {
	if in == nil {
		return nil
	}
	out := make([]sso.ClaimMappingEntry, 0, len(in.Entries))
	for _, e := range in.Entries {
		out = append(out, sso.ClaimMappingEntry{Name: e.Name, Value: e.Value})
	}
	return out
}

// oidcSettingsView is p, an OIDC provider, as the SSO tag answers it: its
// admin login null until a sign-in enabled it, and never its secret.
func oidcSettingsView(p sso.Provider) api.OIDCSettings {
	out := api.OIDCSettings{
		ClientId: p.Settings.ClientID, IdentityProviderHost: p.Settings.IdentityProviderHost,
		ClaimMapping: api.ClaimMapping{Entries: make([]api.ClaimMappingEntry, 0, len(p.Settings.ClaimMapping))},
	}
	for _, e := range p.Settings.ClaimMapping {
		out.ClaimMapping.Entries = append(out.ClaimMapping.Entries, api.ClaimMappingEntry{Name: e.Name, Value: e.Value})
	}
	if p.AdminLogin != "" {
		out.AdminLogin = &p.AdminLogin
	}
	return out
}
