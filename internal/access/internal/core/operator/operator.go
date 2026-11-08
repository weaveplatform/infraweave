package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The organization trail's actions and the resource type they affect.
const (
	ActionCreate          = "organization.create"
	ActionDelete          = "organization.delete"
	ActionBreakGlassSet   = "organization.break_glass_set"
	ActionBreakGlassUnset = "organization.break_glass_delete"
	ResourceOrganization  = "ORGANIZATION"
)

// Organization is an organization as the operator sees it: its accounts,
// oldest first, and its break-glass credential, nil when it has none, with
// a zero Hash.
type Organization struct {
	organization.Organization
	Accounts   []account.Account
	BreakGlass *breakglass.Credential
}

// Store keeps organizations for the operator. Each write records e, its
// organization's trail entry, in its own transaction; an organization
// that does not exist is the store's not found error, and a slug in use
// its slug taken error, which the service passes through.
type Store interface {
	// CreateOrganization records org, its first account and, when bg is
	// not nil, its break-glass credential, in one transaction.
	CreateOrganization(
		ctx context.Context,
		org organization.Organization,
		first account.Account,
		bg *breakglass.Credential,
		e kernel.OrganizationAuditEntry,
	) error
	// Organizations are up to limit organizations in ID order from the
	// first after after; a zero after starts at the first.
	Organizations(
		ctx context.Context,
		after kernel.OrganizationID,
		limit int,
	) ([]Organization, error)
	OrganizationBySlug(ctx context.Context, slug kernel.Slug) (Organization, error)
	// DeleteOrganization deletes the organization, its accounts and what
	// the access domain holds for them, revoking each live session of the
	// organization, and returns those sessions.
	DeleteOrganization(
		ctx context.Context,
		id kernel.OrganizationID,
		e kernel.OrganizationAuditEntry,
	) ([]kernel.ULID, error)
	SetBreakGlass(
		ctx context.Context,
		id kernel.OrganizationID,
		c breakglass.Credential,
		e kernel.OrganizationAuditEntry,
	) error
	DeleteBreakGlass(
		ctx context.Context,
		id kernel.OrganizationID,
		e kernel.OrganizationAuditEntry,
	) error
}

// Deps are what the service needs.
type Deps struct {
	Store Store
	// Drop drops a revoked session from this replica's cache at once; the
	// other replicas drop it on its invalidation.
	Drop func(kernel.ULID)
	Now  func() time.Time
}

// Service is the operator API's organizations. Its callers are operator
// principals, which the operator authenticator alone admits.
//
// ref: decision 0021
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Actor is who calls the service: the principal and the client's address,
// which each write's trail entry records.
type Actor struct {
	Principal kernel.Principal
	RemoteIP  string
}

// Input is a new organization and its first account.
type Input struct {
	Slug, Name               string
	AccountSlug, AccountName string
	// BreakGlass is the organization's credential, or nil for none yet.
	BreakGlass *BreakGlassInput
}

// BreakGlassInput is a break-glass credential as the operator sends it:
// the login and the hash in the format breakglass parses.
//
// ref: decision 0006 § "Human login"
type BreakGlassInput struct {
	Login, PasswordHash string
}

// Create records an organization with its first account, unclaimed, and
// its break-glass credential when in names one. Invalid input is
// organization.ErrInvalid or breakglass.ErrInvalid, naming each field.
func (s *Service) Create(ctx context.Context, by Actor, in Input) (Organization, error) {
	org, oerr := organization.New(in.Slug, in.Name)
	first, aerr := account.New(org.ID, "account.", in.AccountSlug, in.AccountName)
	errs := []error{oerr, aerr}
	var bg *breakglass.Credential
	if in.BreakGlass != nil {
		c, err := s.credential("breakGlass.", *in.BreakGlass)
		bg, errs = &c, append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return Organization{}, err
	}
	now := s.d.Now()
	org.CreatedAt, first.CreatedAt = now, now
	body, _ := json.Marshal(map[string]any{ //nolint:errchkjson // strings only
		"slug": org.Slug, "name": org.Name,
		"account": map[string]any{"slug": first.Slug, "name": first.Name},
	})
	if err := s.d.Store.CreateOrganization(ctx, org, first, bg,
		by.entry(org, ActionCreate, "CREATE", body, now)); err != nil {
		return Organization{}, err //nolint:wrapcheck // the store names the write
	}
	out := Organization{Organization: org, Accounts: []account.Account{first}}
	if bg != nil {
		out.BreakGlass = &breakglass.Credential{Login: bg.Login, UpdatedAt: now}
	}
	return out, nil
}

// List is up to limit organizations from the first after after.
func (s *Service) List(
	ctx context.Context,
	after kernel.OrganizationID,
	limit int,
) ([]Organization, error) {
	return s.d.Store.Organizations(ctx, after, limit) //nolint:wrapcheck // as above
}

// Get reads the organization slug names; a slug that cannot be one is not
// found, as one no organization holds is.
func (s *Service) Get(ctx context.Context, slug string) (Organization, error) {
	sl, err := organization.ParseSlug("organization", slug)
	if err != nil {
		return Organization{}, errNotFound(slug)
	}
	return s.d.Store.OrganizationBySlug(ctx, sl) //nolint:wrapcheck // as above
}

// Delete deletes the organization slug names, its accounts and what the
// access domain holds for them, and returns it as it was. Every session
// it held stops at once on this replica and on its invalidation on the
// others.
func (s *Service) Delete(ctx context.Context, by Actor, slug string) (Organization, error) {
	o, err := s.Get(ctx, slug)
	if err != nil {
		return Organization{}, err
	}
	ids, err := s.d.Store.DeleteOrganization(ctx, o.ID,
		by.entry(o.Organization, ActionDelete, "DELETE", nil, s.d.Now()))
	if err != nil {
		return Organization{}, err //nolint:wrapcheck // as above
	}
	for _, id := range ids {
		s.d.Drop(id)
	}
	return o, nil
}

// SetBreakGlass sets or replaces the break-glass credential of the
// organization slug names and returns the organization with it.
func (s *Service) SetBreakGlass(
	ctx context.Context,
	by Actor,
	slug string,
	in BreakGlassInput,
) (Organization, error) {
	o, err := s.Get(ctx, slug)
	if err != nil {
		return Organization{}, err
	}
	c, err := s.credential("", in)
	if err != nil {
		return Organization{}, err
	}
	body, _ := json.Marshal(map[string]string{"login": c.Login}) //nolint:errchkjson // a string
	if err := s.d.Store.SetBreakGlass(ctx, o.ID, c,
		by.entry(o.Organization, ActionBreakGlassSet, "UPDATE", body, c.UpdatedAt)); err != nil {
		return Organization{}, err //nolint:wrapcheck // as above
	}
	o.BreakGlass = &breakglass.Credential{Login: c.Login, UpdatedAt: c.UpdatedAt}
	return o, nil
}

// DeleteBreakGlass removes the break-glass credential of the organization
// slug names, so no break-glass sign-in works until one is set; removing
// none is no change.
func (s *Service) DeleteBreakGlass(
	ctx context.Context,
	by Actor,
	slug string,
) (Organization, error) {
	o, err := s.Get(ctx, slug)
	if err != nil {
		return Organization{}, err
	}
	if err := s.d.Store.DeleteBreakGlass(ctx, o.ID,
		by.entry(o.Organization, ActionBreakGlassUnset, "UPDATE", nil, s.d.Now())); err != nil {
		return Organization{}, err //nolint:wrapcheck // as above
	}
	o.BreakGlass = nil
	return o, nil
}

// credential checks in, its fields named under prefix.
func (s *Service) credential(prefix string, in BreakGlassInput) (breakglass.Credential, error) {
	login := strings.TrimSpace(in.Login)
	lerr := organization.CheckName(prefix+"login", login)
	h, herr := breakglass.Parse(prefix+"passwordHash", in.PasswordHash)
	if err := errors.Join(lerr, herr); err != nil {
		return breakglass.Credential{}, err
	}
	return breakglass.Credential{Login: login, Hash: h, UpdatedAt: s.d.Now()}, nil
}

// entry is the trail entry of a change to o by a.
func (a Actor) entry(
	o organization.Organization,
	action, eventType string,
	body json.RawMessage,
	at time.Time,
) kernel.OrganizationAuditEntry {
	return kernel.OrganizationAuditEntry{
		OrganizationID:   o.ID,
		ActorKind:        a.Principal.Kind,
		ActorID:          a.Principal.ActorID.String(),
		Action:           action,
		EventType:        eventType,
		AffectedResource: kernel.AuditResource{Type: ResourceOrganization, ID: string(o.Slug)},
		Body:             body,
		RemoteIP:         a.RemoteIP,
		CreatedAt:        at,
	}
}

func errNotFound(slug string) error {
	return fmt.Errorf("%w: organization %q", port.ErrNotFound, slug)
}
