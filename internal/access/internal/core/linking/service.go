package linking

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// LinkPathPrefix is the path of a link's page without the link's ID.
const LinkPathPrefix = "/auth/link/"

// Store keeps the links and the identities they prove. Everything is
// scoped to the organization, so another organization's member or link is
// port.ErrNotFound.
type Store interface {
	// CreateLink records l and e under the organization's lock: each of
	// its members must be the organization's (port.ErrNotFound) and pass
	// Linkable, and the pair must have no open link (ErrLinkExists); an
	// expired link of the pair is deleted first.
	CreateLink(ctx context.Context, l Link, e kernel.OrganizationAuditEntry) error
	// Link reads the link id names, in any organization, with its
	// organization's slug.
	Link(ctx context.Context, id kernel.ULID) (Link, kernel.Slug, error)
	// Providers are the organization's identity providers, oldest first.
	Providers(ctx context.Context, org kernel.OrganizationID) ([]sso.Provider, error)
	// Identities are the member's identities, oldest first.
	Identities(ctx context.Context, org kernel.OrganizationID, m kernel.MemberID) ([]Federation, error)
	// UnlinkIdentities removes the member's identities PlanUnlink chooses
	// for kind, the identity the session sess signed in through kept,
	// revokes the sessions signed in through them, records e with
	// UnlinkBody, and answers the identities that remain and the revoked
	// sessions, in one transaction.
	UnlinkIdentities(
		ctx context.Context,
		org kernel.OrganizationID,
		m kernel.MemberID,
		sess kernel.ULID,
		kind sso.Kind,
		e kernel.OrganizationAuditEntry,
	) ([]Federation, []kernel.ULID, error)
}

// SignIn starts a sign-in through an organization's provider; sso.SignIn
// is one.
type SignIn interface {
	Start(ctx context.Context, in sso.Start) (sso.Started, error)
}

// Deps are what the service needs.
type Deps struct {
	Store  Store
	Admins sso.Admins
	SignIn SignIn
	// Drop drops a revoked session from this replica's cache at once.
	Drop func(kernel.ULID)
	// PublicURL is server.public_url, a link's page's base.
	PublicURL string
	Now       func() time.Time
}

// Service links two members into one on an organization admin's link and
// the person's sign-in through each, and removes a member's own
// identities. Nothing is ever linked by e-mail address.
//
// ref: decision 0006 § "Members and identity providers"
type Service struct{ d Deps }

// NewService returns the service on d.
func NewService(d Deps) *Service { return &Service{d: d} }

// Create opens a link that merges the member other names into the member
// id names, for an organization admin. An ID that cannot be a member's,
// like a member of another organization, is port.ErrNotFound.
func (s *Service) Create(ctx context.Context, by orgadmin.Actor, id, other string) (Link, error) {
	if err := s.d.Admins.Admit(ctx, by); err != nil {
		return Link{}, err //nolint:wrapcheck // orgadmin.ErrForbidden, answered as it is
	}
	m, err := memberID(id)
	if err != nil {
		return Link{}, err
	}
	o, err := memberID(other)
	if err != nil {
		return Link{}, err
	}
	now := s.d.Now()
	p := by.Principal
	l := Link{
		OrganizationID: p.OrganizationID, ID: kernel.NewULID(), MemberID: m, OtherMemberID: o,
		CreatedBy: string(p.Kind) + ":" + p.ActorID.String(),
		CreatedAt: now, ExpiresAt: now.Add(TTL),
	}
	b, _ := json.Marshal(map[string]string{ //nolint:errchkjson // strings only
		"linkId": l.ID.String(), "otherMemberId": o.String(),
	})
	e := kernel.OrganizationAuditEntry{
		OrganizationID: p.OrganizationID, ActorKind: p.Kind, ActorID: p.ActorID.String(),
		Action: ActionCreate, EventType: "CREATE",
		AffectedResource: kernel.AuditResource{Type: ResourceMember, ID: m.String()},
		Body:             b, RemoteIP: by.RemoteIP, CreatedAt: now,
	}
	if err := s.d.Store.CreateLink(ctx, l, e); err != nil {
		return Link{}, err //nolint:wrapcheck // the store names the write and its refusals
	}
	return l, nil
}

// URL is where the person proves l: its page on the public URL.
func (s *Service) URL(l Link) string {
	return strings.TrimSuffix(s.d.PublicURL, "/") + LinkPathPrefix + l.ID.String()
}

// Page is what a link's page shows: the link, its organization, and the
// enabled OIDC providers each member holds an identity at, oldest first.
type Page struct {
	Link         Link
	Organization kernel.Slug
	Member       []sso.Provider
	Other        []sso.Provider
}

// Page reads the link id names for its page. An ID that cannot be a
// link's is port.ErrNotFound; a link that expired without completing is
// ErrEnded, while a completed one still shows.
func (s *Service) Page(ctx context.Context, id string) (Page, error) {
	u, err := kernel.ParseULID(id)
	if err != nil {
		return Page{}, fmt.Errorf("%w: link %q", port.ErrNotFound, id)
	}
	l, slug, err := s.d.Store.Link(ctx, u)
	if err != nil {
		return Page{}, err //nolint:wrapcheck // the store names the read
	}
	if l.CompletedAt.IsZero() && !l.Open(s.d.Now()) {
		return Page{}, fmt.Errorf("%w: link %s", ErrEnded, l.ID)
	}
	out := Page{Link: l, Organization: slug}
	if !l.CompletedAt.IsZero() {
		return out, nil
	}
	ps, err := s.d.Store.Providers(ctx, l.OrganizationID)
	if err != nil {
		return Page{}, err //nolint:wrapcheck // the store names the read
	}
	if out.Member, err = s.providersOf(ctx, l.OrganizationID, l.MemberID, ps); err != nil {
		return Page{}, err
	}
	if out.Other, err = s.providersOf(ctx, l.OrganizationID, l.OtherMemberID, ps); err != nil {
		return Page{}, err
	}
	return out, nil
}

// providersOf are the enabled OIDC providers of ps that m holds an
// identity at, in ps's order.
func (s *Service) providersOf(ctx context.Context, org kernel.OrganizationID, m kernel.MemberID, ps []sso.Provider) ([]sso.Provider, error) {
	fs, err := s.d.Store.Identities(ctx, org, m)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	var out []sso.Provider
	for _, p := range ps {
		held := slices.ContainsFunc(fs, func(f Federation) bool { return f.ProviderID == p.ID })
		if held && p.Kind == sso.OIDC && p.Enabled() {
			out = append(out, p)
		}
	}
	return out, nil
}

// Start starts a link's sign-in through provider, one of the providers
// its page shows, with a fresh authentication that returns the browser to
// the page. by is the browser's live session's principal, nil for none.
func (s *Service) Start(ctx context.Context, id, provider string, by *kernel.Principal) (sso.Started, error) {
	pg, err := s.Page(ctx, id)
	if err != nil {
		return sso.Started{}, err
	}
	if !pg.Link.CompletedAt.IsZero() {
		return sso.Started{}, fmt.Errorf("%w: link %s", ErrEnded, pg.Link.ID)
	}
	offered := func(p sso.Provider) bool { return p.ID.String() == provider }
	if !slices.ContainsFunc(pg.Member, offered) && !slices.ContainsFunc(pg.Other, offered) {
		return sso.Started{}, fmt.Errorf("%w: link %s through %q", port.ErrNotFound, pg.Link.ID, provider)
	}
	//nolint:wrapcheck // the sign-in names its refusals
	return s.d.SignIn.Start(ctx, sso.Start{
		Organization: string(pg.Organization), Provider: provider, Purpose: string(sso.PurposeLink),
		ReturnTo: LinkPathPrefix + pg.Link.ID.String(), Link: pg.Link.ID, By: by,
	})
}

// Identity is p's member's identities, nil for a principal that names no
// member.
func (s *Service) Identity(ctx context.Context, p kernel.Principal) (*Identity, error) {
	if p.MemberID.IsZero() {
		return nil, nil //nolint:nilnil // no member has no identities
	}
	fs, err := s.d.Store.Identities(ctx, p.OrganizationID, p.MemberID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	return &Identity{MemberID: p.MemberID, Federations: fs}, nil
}

// Unlink removes p's member's identities at providers of the kind
// provider names, OIDC, SAML or GITHUB_OAUTH, keeping the one p's session
// signed in through, and revokes the sessions signed in through them, on
// this replica at once. It answers the identities that remain. A
// principal that names no member, such as the break-glass admin's, an
// organization key's or an API key's that is not personal, is
// ErrNoMember; another provider is a FieldError naming provider.
//
// ref: report 14 section 6.5
func (s *Service) Unlink(ctx context.Context, p kernel.Principal, ip, provider string) (Identity, error) {
	if p.MemberID.IsZero() {
		return Identity{}, ErrNoMember
	}
	kind, err := sso.ParseKind(provider)
	if err != nil {
		return Identity{}, &organization.FieldError{
			Field: "provider", Reason: "is not OIDC, SAML or GITHUB_OAUTH",
		}
	}
	e := kernel.OrganizationAuditEntry{
		OrganizationID: p.OrganizationID, ActorKind: p.Kind, ActorID: p.ActorID.String(),
		Action: ActionUnlink, EventType: "DELETE",
		AffectedResource: kernel.AuditResource{Type: ResourceIdentity, ID: p.MemberID.String()},
		RemoteIP:         ip, CreatedAt: s.d.Now(),
	}
	left, revoked, err := s.d.Store.UnlinkIdentities(ctx, p.OrganizationID, p.MemberID, p.SessionID, kind, e)
	if err != nil {
		return Identity{}, err //nolint:wrapcheck // the store names the write and its refusals
	}
	for _, id := range revoked {
		s.d.Drop(id)
	}
	return Identity{MemberID: p.MemberID, Federations: left}, nil
}

// memberID parses a member's ID; one that cannot be one names no member.
func memberID(s string) (kernel.MemberID, error) {
	u, err := kernel.ParseULID(s)
	if err != nil {
		return kernel.MemberID{}, fmt.Errorf("%w: member %q", port.ErrNotFound, s)
	}
	return kernel.MemberID{ULID: u}, nil
}
