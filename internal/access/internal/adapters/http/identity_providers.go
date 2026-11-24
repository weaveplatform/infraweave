package http

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// OrganizationIdentityProviders lists the organization's identity
// providers, oldest first, with no secret.
func (s Server) OrganizationIdentityProviders(ctx context.Context, _ api.OrganizationIdentityProvidersRequestObject) (api.OrganizationIdentityProvidersResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := s.providers.List(ctx, by)
	if err != nil {
		return nil, providerError(err, "")
	}
	body := api.OrganizationIdentityProviderList{
		Items: make([]api.OrganizationIdentityProvider, 0, len(ps)),
	}
	for _, p := range ps {
		body.Items = append(body.Items, providerView(p))
	}
	return api.OrganizationIdentityProviders200JSONResponse{Body: body}, nil
}

// OrganizationIdentityProviderCreate creates an identity provider,
// disabled.
func (s Server) OrganizationIdentityProviderCreate(
	ctx context.Context,
	req api.OrganizationIdentityProviderCreateRequestObject,
) (api.OrganizationIdentityProviderCreateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.providers.Create(ctx, by, sso.Input{
		Kind: string(req.Body.Kind), Name: req.Body.Name,
		Settings: settingsInput(req.Body.Settings), Secret: req.Body.Secret,
	})
	if err != nil {
		return nil, providerError(err, "")
	}
	return api.OrganizationIdentityProviderCreate201JSONResponse{Body: providerView(p)}, nil
}

// OrganizationIdentityProviderUpdate changes an identity provider.
func (s Server) OrganizationIdentityProviderUpdate(
	ctx context.Context,
	req api.OrganizationIdentityProviderUpdateRequestObject,
) (api.OrganizationIdentityProviderUpdateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	patch := sso.Patch{Kind: (*string)(req.Body.Kind), Name: req.Body.Name, Secret: req.Body.Secret}
	if req.Body.Settings != nil {
		in := settingsInput(*req.Body.Settings)
		patch.Settings = &in
	}
	p, err := s.providers.Update(ctx, by, req.ProviderId, patch)
	if err != nil {
		return nil, providerError(err, req.ProviderId)
	}
	return api.OrganizationIdentityProviderUpdate200JSONResponse{Body: providerView(p)}, nil
}

// OrganizationIdentityProviderDelete deletes an identity provider and
// answers it as it was.
func (s Server) OrganizationIdentityProviderDelete(
	ctx context.Context,
	req api.OrganizationIdentityProviderDeleteRequestObject,
) (api.OrganizationIdentityProviderDeleteResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.providers.Delete(ctx, by, req.ProviderId)
	if err != nil {
		return nil, providerError(err, req.ProviderId)
	}
	return api.OrganizationIdentityProviderDelete200JSONResponse{Body: providerView(p)}, nil
}

// OrganizationIdentityProviderSetDefault makes an identity provider the
// default of its kind.
func (s Server) OrganizationIdentityProviderSetDefault(
	ctx context.Context,
	req api.OrganizationIdentityProviderSetDefaultRequestObject,
) (api.OrganizationIdentityProviderSetDefaultResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.providers.SetDefault(ctx, by, req.ProviderId)
	if err != nil {
		return nil, providerError(err, req.ProviderId)
	}
	return api.OrganizationIdentityProviderSetDefault200JSONResponse{Body: providerView(p)}, nil
}

// providerError is orgAdminError for the identity provider id, and 403
// feature-disabled or feature-not-in-tier for a kind whose feature the
// organization may not use.
func providerError(err error, id string) error {
	if r, ok := errors.AsType[*featuresapi.Refusal](err); ok {
		p := problem.FeatureNotInTier.New(fmt.Sprintf("The organization's tier does not include %s.", r.Feature))
		p.Feature, p.Tiers = r.Feature, r.Tiers
		if r.Disabled {
			p = problem.FeatureDisabled.New(fmt.Sprintf("The operator has switched off %s for this installation.", r.Feature))
			p.Feature = r.Feature
		}
		return &problem.Error{Problem: p}
	}
	what := "identity provider"
	if id != "" {
		what = fmt.Sprintf("identity provider %q", id)
	}
	return orgAdminError(err, what)
}

// settingsInput is in as the service takes it.
func settingsInput(in api.OrganizationIdentityProviderSettingsInput) sso.SettingsInput {
	out := sso.SettingsInput{
		IdentityProviderHost: in.IdentityProviderHost, ClientID: in.ClientId,
		IDPMetadata: in.IdpMetadata, IDPMetadataURL: in.IdpMetadataURL,
		NameIDFormat: in.NameIDFormat, BaseURL: in.BaseUrl, Organization: in.Organization,
	}
	if in.ClaimMapping != nil {
		entries := make([]sso.ClaimMappingEntry, 0, len(in.ClaimMapping.Entries))
		for _, e := range in.ClaimMapping.Entries {
			entries = append(entries, sso.ClaimMappingEntry{Name: e.Name, Value: e.Value})
		}
		out.ClaimMapping = &entries
	}
	return out
}

// providerView is p in the tag's shape: its kind's settings and never its
// secret.
func providerView(p sso.Provider) api.OrganizationIdentityProvider {
	out := api.OrganizationIdentityProvider{
		Id: p.ID.String(), Kind: api.OrganizationIdentityProviderKind(p.Kind), Name: p.Name,
		IsDefault: p.Default, Enabled: p.Enabled(), CreatedAt: p.CreatedAt.Unix(),
		Settings: settingsView(p.Kind, p.Settings),
	}
	if p.AdminLogin != "" {
		out.AdminLogin = &p.AdminLogin
	}
	return out
}

// settingsView is s, the settings of a provider of kind k, as the tag
// answers them: each of k's settings, empty or not, and the claim mapping
// of a kind that takes one.
func settingsView(k sso.Kind, s sso.Settings) api.OrganizationIdentityProviderSettings {
	var out api.OrganizationIdentityProviderSettings
	optional := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	mapping := func() *api.ClaimMapping {
		m := api.ClaimMapping{Entries: make([]api.ClaimMappingEntry, 0, len(s.ClaimMapping))}
		for _, e := range s.ClaimMapping {
			m.Entries = append(m.Entries, api.ClaimMappingEntry{Name: e.Name, Value: e.Value})
		}
		return &m
	}
	switch k {
	case sso.OIDC:
		out.IdentityProviderHost, out.ClientId = &s.IdentityProviderHost, &s.ClientID
		out.ClaimMapping = mapping()
	case sso.SAML:
		nameID := string(s.NameIDFormat)
		out.IdpMetadata, out.IdpMetadataURL = optional(s.IDPMetadata), optional(s.IDPMetadataURL)
		out.NameIDFormat, out.ClaimMapping = &nameID, mapping()
	case sso.GitHubOAuth:
		out.BaseUrl, out.ClientId, out.Organization = &s.BaseURL, &s.ClientID, &s.Organization
	}
	return out
}
