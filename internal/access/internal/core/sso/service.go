package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The organization trail's actions on identity providers, and the resource
// type they affect.
const (
	ActionCreate     = "identity_provider.create"
	ActionUpdate     = "identity_provider.update"
	ActionDelete     = "identity_provider.delete"
	ActionSetDefault = "identity_provider.set_default"
	ResourceProvider = "IDENTITY_PROVIDER"
)

// Store keeps an organization's identity providers. Each write records e,
// its organization trail entry, in its own transaction, and sees the
// organization's providers under a lock its other writes take, so the
// default per kind stays one. Everything is scoped to the organization, so
// another organization's provider is port.ErrNotFound.
type Store interface {
	// Providers are org's providers, oldest first.
	Providers(ctx context.Context, org kernel.OrganizationID) ([]Provider, error)
	Provider(ctx context.Context, org kernel.OrganizationID, id kernel.ULID) (Provider, error)
	// CreateProvider inserts p, its kind's default when FirstOfKind says
	// so, with secret sealed, and returns it as stored.
	CreateProvider(ctx context.Context, p Provider, secret []byte, e kernel.OrganizationAuditEntry) (Provider, error)
	// CreateDefaultProvider inserts p as its kind's default, with secret
	// sealed, and returns it as stored; an organization holding a provider
	// of p's kind is ErrDefaultExists.
	CreateDefaultProvider(ctx context.Context, p Provider, secret []byte, e kernel.OrganizationAuditEntry) (Provider, error)
	// UpdateProvider writes p's name, settings and update time, and secret
	// unless it is nil, and returns p as stored.
	UpdateProvider(ctx context.Context, p Provider, secret []byte, e kernel.OrganizationAuditEntry) (Provider, error)
	// DeleteProvider deletes the provider and the identities that sign in
	// through it, revokes every session signed in through it, which it
	// returns, and makes Successor its kind's default when it was. It
	// returns the provider as it was.
	DeleteProvider(ctx context.Context, org kernel.OrganizationID, id kernel.ULID, e kernel.OrganizationAuditEntry) (Provider, []kernel.ULID, error)
	// SetDefaultProvider makes the provider its kind's default in place of
	// the one that was, and returns it.
	SetDefaultProvider(ctx context.Context, org kernel.OrganizationID, id kernel.ULID, e kernel.OrganizationAuditEntry) (Provider, error)
}

// Admins admit the organization's admins; orgadmin.Service is one.
type Admins interface {
	Admit(ctx context.Context, by orgadmin.Actor) error
}

// Deps are what the service needs.
type Deps struct {
	Store  Store
	Admins Admins
	// Features checks the feature a provider's kind needs; nil checks none,
	// as billing off admits every feature.
	Features featuresapi.Options
	// Discovery reads an OIDC provider's discovery document when the SSO
	// tag creates one.
	Discovery Discovery
	// Drop drops a revoked session from this replica's cache at once.
	Drop func(kernel.ULID)
	Now  func() time.Time
}

// Service manages the organization's identity providers for the
// Organization tag: it lists, creates, changes and deletes them and
// chooses the default per kind. For the SSO tag, it reads, creates,
// changes and deletes the default OIDC provider and answers whether the
// organization has single sign-on.
//
// ref: decisions 0006 and 0021
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Input is a new provider: its kind, name and settings, and the OIDC or
// GitHub OAuth client secret.
type Input struct {
	Kind     string
	Name     string
	Settings SettingsInput
	Secret   *string
}

// Patch is a change of a provider, nil for each member it leaves out.
type Patch struct {
	Kind     *string
	Name     *string
	Settings *SettingsInput
	Secret   *string
}

// List is every provider of the caller's organization, oldest first.
func (s *Service) List(ctx context.Context, by orgadmin.Actor) ([]Provider, error) {
	if err := s.d.Admins.Admit(ctx, by); err != nil {
		return nil, err //nolint:wrapcheck // orgadmin.ErrForbidden, answered as it is
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.Providers(ctx, by.Principal.OrganizationID)
}

// Create creates a provider, disabled. The kind's feature refused is a
// *featuresapi.Refusal; invalid input is organization.ErrInvalid, a
// FieldError naming each field. A SAML provider takes no secret: its
// service provider signing key is generated.
func (s *Service) Create(ctx context.Context, by orgadmin.Actor, in Input) (Provider, error) {
	if err := s.d.Admins.Admit(ctx, by); err != nil {
		return Provider{}, err //nolint:wrapcheck // as above
	}
	k, err := ParseKind(in.Kind)
	if err != nil {
		return Provider{}, err
	}
	if s.d.Features != nil {
		if err := s.d.Features.AllowAccount(ctx, by.Principal.OrganizationID,
			by.Principal.AccountID, k.Feature()); err != nil {
			return Provider{}, err //nolint:wrapcheck // a *featuresapi.Refusal, answered as it is
		}
	}
	settings, serr := NewSettings(k, in.Settings)
	if err := errors.Join(organization.CheckName("name", in.Name), serr, checkSecret(k, in.Secret, true)); err != nil {
		return Provider{}, err
	}
	secret := []byte(nil)
	if k == SAML {
		if secret, err = NewSigningKey(); err != nil {
			return Provider{}, err
		}
	} else {
		secret = []byte(*in.Secret)
	}
	now := s.d.Now()
	org := by.Principal.OrganizationID
	p := Provider{
		OrganizationID: org, ID: kernel.NewULID(), Kind: k, Name: in.Name, Settings: settings,
		CreatedAt: now, UpdatedAt: now,
	}
	//nolint:wrapcheck // the store names the write
	return s.d.Store.CreateProvider(ctx, p, secret, entry(by, ActionCreate, "CREATE", p.ID,
		body(map[string]any{"kind": k, "name": p.Name, "settings": settings}), now))
}

// Update changes the provider id names: its name, claim mapping, secret,
// or a SAML provider's metadata or metadata URL. A kind or immutable
// setting other than the provider's is organization.ErrInvalid, as is
// invalid input.
func (s *Service) Update(ctx context.Context, by orgadmin.Actor, id string, patch Patch) (Provider, error) {
	p, err := s.provider(ctx, by, id)
	if err != nil {
		return Provider{}, err
	}
	var errs []error
	if patch.Kind != nil && Kind(*patch.Kind) != p.Kind {
		errs = append(errs, &organization.FieldError{
			Field: "kind", Reason: "never changes; delete the provider and create another",
		})
	}
	changed := map[string]any{}
	if patch.Name != nil {
		errs = append(errs, organization.CheckName("name", *patch.Name))
		p.Name, changed["name"] = *patch.Name, *patch.Name
	}
	if patch.Settings != nil {
		settings, err := p.Settings.Change(p.Kind, *patch.Settings)
		errs = append(errs, err)
		p.Settings, changed["settings"] = settings, settings
	}
	errs = append(errs, checkSecret(p.Kind, patch.Secret, false))
	if err := errors.Join(errs...); err != nil {
		return Provider{}, err
	}
	var secret []byte
	if patch.Secret != nil {
		secret, changed["secret"] = []byte(*patch.Secret), "changed"
	}
	now := s.d.Now()
	p.UpdatedAt = now
	//nolint:wrapcheck // the store names the write
	return s.d.Store.UpdateProvider(ctx, p, secret, entry(by, ActionUpdate, "UPDATE", p.ID,
		body(changed), now))
}

// Delete deletes the provider id names with its identities and returns it
// as it was. The sessions signed in through it stop at once on this
// replica and on their invalidation on the others.
func (s *Service) Delete(ctx context.Context, by orgadmin.Actor, id string) (Provider, error) {
	u, err := s.id(ctx, by, id)
	if err != nil {
		return Provider{}, err
	}
	return s.delete(ctx, by, u)
}

// delete deletes the provider id of by's organization and drops the
// sessions the store revoked.
func (s *Service) delete(ctx context.Context, by orgadmin.Actor, id kernel.ULID) (Provider, error) {
	p, ids, err := s.d.Store.DeleteProvider(ctx, by.Principal.OrganizationID, id,
		entry(by, ActionDelete, "DELETE", id, nil, s.d.Now()))
	if err != nil {
		return Provider{}, err //nolint:wrapcheck // the store names the write
	}
	for _, id := range ids {
		s.d.Drop(id)
	}
	return p, nil
}

// SetDefault makes the provider id names the default of its kind and
// returns it.
func (s *Service) SetDefault(ctx context.Context, by orgadmin.Actor, id string) (Provider, error) {
	u, err := s.id(ctx, by, id)
	if err != nil {
		return Provider{}, err
	}
	//nolint:wrapcheck // the store names the write
	return s.d.Store.SetDefaultProvider(ctx, by.Principal.OrganizationID, u,
		entry(by, ActionSetDefault, "UPDATE", u, nil, s.d.Now()))
}

// provider reads the provider id names in the caller's organization.
func (s *Service) provider(ctx context.Context, by orgadmin.Actor, id string) (Provider, error) {
	u, err := s.id(ctx, by, id)
	if err != nil {
		return Provider{}, err
	}
	//nolint:wrapcheck // the store names the read
	return s.d.Store.Provider(ctx, by.Principal.OrganizationID, u)
}

// id admits the caller and parses id; an ID that cannot be one is not
// found, as another organization's provider is.
func (s *Service) id(ctx context.Context, by orgadmin.Actor, id string) (kernel.ULID, error) {
	if err := s.d.Admins.Admit(ctx, by); err != nil {
		return kernel.ULID{}, err //nolint:wrapcheck // as above
	}
	u, err := kernel.ParseULID(id)
	if err != nil {
		return kernel.ULID{}, fmt.Errorf("%w: identity provider %q", port.ErrNotFound, id)
	}
	return u, nil
}

// checkSecret refuses a secret a provider of kind k does not take, a SAML
// provider's, whose key is generated, and an empty one; a new OIDC or
// GitHub OAuth provider needs one.
func checkSecret(k Kind, secret *string, create bool) error {
	switch {
	case secret == nil:
		if create && k != SAML {
			return &organization.FieldError{Field: "secret", Reason: "is required"}
		}
		return nil
	case k == SAML:
		return &organization.FieldError{Field: "secret", Reason: "is generated for a SAML provider, which takes none"}
	case strings.TrimSpace(*secret) == "":
		return &organization.FieldError{Field: "secret", Reason: "is empty"}
	}
	return nil
}

// entry is the trail entry of by's change to the provider id.
func entry(by orgadmin.Actor, action, eventType string, id kernel.ULID, b json.RawMessage, at time.Time) kernel.OrganizationAuditEntry {
	p := by.Principal
	return kernel.OrganizationAuditEntry{
		OrganizationID:   p.OrganizationID,
		ActorKind:        p.Kind,
		ActorID:          p.ActorID.String(),
		Action:           action,
		EventType:        eventType,
		AffectedResource: kernel.AuditResource{Type: ResourceProvider, ID: id.String()},
		Body:             b,
		RemoteIP:         by.RemoteIP,
		CreatedAt:        at,
	}
}

// body is the trail entry's body: the changed members, never a secret.
func body(v map[string]any) json.RawMessage {
	b, _ := json.Marshal(v) //nolint:errchkjson // strings, kinds and settings only
	return b
}
