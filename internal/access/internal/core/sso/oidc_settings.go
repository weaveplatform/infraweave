package sso

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrDefaultExists refuses a provider that would be its kind's default
// while the organization holds a provider of the kind. The SSO tag's
// create addresses the default, so a second provider would sit unseen
// beside the one its settings read.
var ErrDefaultExists = errors.New("the organization has a default provider of the kind")

// Discovery reads a provider's discovery document; RelyingParty is one.
type Discovery interface {
	Discover(ctx context.Context, host string) (Endpoints, error)
}

// OIDCInput is the SSO tag's new default OIDC provider: its issuer or
// discovery URL, its client and the claim mapping, nil for none.
type OIDCInput struct {
	IdentityProviderHost string
	ClientID             string
	ClientCredentials    string
	ClaimMapping         []ClaimMappingEntry
}

// OIDCSettings is the default OIDC provider of the caller's organization;
// an organization without one is port.ErrNotFound.
//
// ref: decision 0006 § "Members and identity providers"
func (s *Service) OIDCSettings(ctx context.Context, by orgadmin.Actor) (Provider, error) {
	if err := s.d.Admins.Admit(ctx, by); err != nil {
		return Provider{}, err //nolint:wrapcheck // orgadmin.ErrForbidden, answered as it is
	}
	return s.defaultOIDC(ctx, by)
}

// CreateOIDC creates the caller's organization's default OIDC provider,
// disabled, named after its issuer's host. Invalid input is a FieldError
// naming the SSO tag's field; an organization with a default OIDC provider
// is ErrDefaultExists; a host whose discovery document cannot be read, or
// names another issuer, is a FieldError naming identityProviderHost. The
// document is read after every other check, so a refused request reaches
// no provider.
func (s *Service) CreateOIDC(ctx context.Context, by orgadmin.Actor, in OIDCInput) (Provider, error) {
	if err := s.d.Admins.Admit(ctx, by); err != nil {
		return Provider{}, err //nolint:wrapcheck // as above
	}
	settings, err := NewSettings(OIDC, SettingsInput{
		IdentityProviderHost: &in.IdentityProviderHost, ClientID: &in.ClientID, ClaimMapping: &in.ClaimMapping,
	})
	if err := errors.Join(err, checkSecret(OIDC, &in.ClientCredentials, true)); err != nil {
		return Provider{}, tagFields(err)
	}
	switch _, err := s.defaultOIDC(ctx, by); {
	case err == nil:
		return Provider{}, fmt.Errorf("%w: an OIDC provider; delete it to create another", ErrDefaultExists)
	case !errors.Is(err, port.ErrNotFound):
		return Provider{}, err
	}
	if _, err := s.d.Discovery.Discover(ctx, settings.IdentityProviderHost); err != nil {
		return Provider{}, &organization.FieldError{
			Field: fieldHost, Reason: fmt.Sprintf("has no usable discovery document: %v", err),
		}
	}
	now := s.d.Now()
	p := Provider{
		OrganizationID: by.Principal.OrganizationID, ID: kernel.NewULID(), Kind: OIDC,
		Name: hostName(settings.IdentityProviderHost), Settings: settings, CreatedAt: now, UpdatedAt: now,
	}
	//nolint:wrapcheck // the store names the write
	return s.d.Store.CreateDefaultProvider(ctx, p, []byte(in.ClientCredentials), entry(by, ActionCreate, "CREATE", p.ID,
		body(map[string]any{"kind": OIDC, "name": p.Name, "settings": settings}), now))
}

// UpdateOIDC replaces the claim mapping of the caller's organization's
// default OIDC provider with mapping, nil restoring every target's
// default, and leaves whether it is enabled as it was. An invalid entry is
// a FieldError naming the SSO tag's field.
func (s *Service) UpdateOIDC(ctx context.Context, by orgadmin.Actor, mapping []ClaimMappingEntry) (Provider, error) {
	p, err := s.OIDCSettings(ctx, by)
	if err != nil {
		return Provider{}, err
	}
	settings, err := p.Settings.Change(OIDC, SettingsInput{ClaimMapping: &mapping})
	if err != nil {
		return Provider{}, tagFields(err)
	}
	now := s.d.Now()
	p.Settings, p.UpdatedAt = settings, now
	//nolint:wrapcheck // the store names the write
	return s.d.Store.UpdateProvider(ctx, p, nil, entry(by, ActionUpdate, "UPDATE", p.ID,
		body(map[string]any{"settings": settings}), now))
}

// DeleteOIDC deletes the caller's organization's default OIDC provider as
// Delete does, unlinking single sign-on through it: its identities go and
// its sessions end, and the oldest remaining OIDC provider becomes the
// default.
//
// ref: decision 0006 § "Sessions"
func (s *Service) DeleteOIDC(ctx context.Context, by orgadmin.Actor) (Provider, error) {
	p, err := s.OIDCSettings(ctx, by)
	if err != nil {
		return Provider{}, err
	}
	return s.delete(ctx, by, p.ID)
}

// HasSSO reports whether the caller's organization has an enabled OIDC or
// SAML provider. GitHub OAuth is the organization's own sign-in, not
// single sign-on through its identity provider.
func (s *Service) HasSSO(ctx context.Context, by orgadmin.Actor) (bool, error) {
	ps, err := s.List(ctx, by)
	if err != nil {
		return false, err
	}
	for _, p := range ps {
		if (p.Kind == OIDC || p.Kind == SAML) && p.Enabled() {
			return true, nil
		}
	}
	return false, nil
}

// defaultOIDC reads the default OIDC provider of by's organization.
func (s *Service) defaultOIDC(ctx context.Context, by orgadmin.Actor) (Provider, error) {
	ps, err := s.d.Store.Providers(ctx, by.Principal.OrganizationID)
	if err != nil {
		return Provider{}, err //nolint:wrapcheck // the store names the read
	}
	for _, p := range ps {
		if p.Kind == OIDC && p.Default {
			return p, nil
		}
	}
	return Provider{}, fmt.Errorf("%w: OIDC provider", port.ErrNotFound)
}

// hostName is the host of host, an https URL Check accepted, cut to a
// display name's length.
func hostName(host string) string {
	u, _ := url.Parse(host) //nolint:errcheck // Check parsed it
	name := []rune(u.Host)
	return string(name[:min(len(name), organization.MaxName)])
}

// tagFields is err with each FieldError naming the SSO tag's field: its
// settings are members of the body itself, and the secret is
// clientCredentials.
func tagFields(err error) error {
	all := leaves(err)
	for i, e := range all {
		var fe *organization.FieldError
		if errors.As(e, &fe) {
			field := strings.TrimPrefix(fe.Field, "settings.")
			if field == "secret" {
				field = "clientCredentials"
			}
			all[i] = &organization.FieldError{Field: field, Reason: fe.Reason}
		}
	}
	return errors.Join(all...)
}

// leaves are the errors err joins, through nested joins, or err itself.
func leaves(err error) []error {
	j, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // a join's own errors, not its chain
	if !ok {
		return []error{err}
	}
	var out []error
	for _, e := range j.Unwrap() {
		out = append(out, leaves(e)...)
	}
	return out
}
