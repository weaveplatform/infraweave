package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
)

// ActionEnable is the organization trail's action of an enabling sign-in.
const ActionEnable = "identity_provider.enable"

// SignInStore keeps what an OIDC sign-in reads and writes.
type SignInStore interface {
	// SignInOrganization reads the organization slug names.
	SignInOrganization(ctx context.Context, slug kernel.Slug) (organization.Organization, error)
	// SoleOrganization is the installation's only organization's slug,
	// and false when it has none or several.
	SoleOrganization(ctx context.Context) (kernel.Slug, bool, error)
	Providers(ctx context.Context, org kernel.OrganizationID) ([]Provider, error)
	Provider(ctx context.Context, org kernel.OrganizationID, id kernel.ULID) (Provider, error)
	// ProviderSecret opens the provider's client secret.
	ProviderSecret(ctx context.Context, org kernel.OrganizationID, id kernel.ULID) (crypt.Value, error)
	// CreateSignInRequest stores r with its verifier sealed.
	CreateSignInRequest(ctx context.Context, r Request) error
	// ConsumeSignInRequest marks the live request whose state and browser cookie
	// hash to stateHash and browserHash consumed and returns it; none, a
	// consumed one or an expired one is port.ErrNotFound. Two callbacks
	// of one request consume it once.
	ConsumeSignInRequest(ctx context.Context, stateHash, browserHash []byte) (Request, error)
	// SignInIdentity resolves rec's identity to a member by ResolveMember, checks
	// a new member's seat, records the identity, sets the member's
	// last_seen_at, and creates rec.Session for it, replacing the member's
	// live WEB session, with rec.IDToken sealed beside it, all in one
	// transaction; with rec.Enable, it also enables the provider with the
	// identity's login as its admin login and records the entry. A
	// re-authentication's rec.Member that the identity does not resolve
	// to is ErrOtherMember; a full seats limit a *featuresapi.LimitError.
	SignInIdentity(ctx context.Context, rec SignInRecord) (session.Session, error)
	// ProvidersByClientID are the OIDC providers of every organization
	// whose client ID is one of ids.
	ProvidersByClientID(ctx context.Context, ids []string) ([]Provider, error)
	// AcceptLogoutToken records the provider's logout token jti until
	// expires, and reports false when it is recorded already.
	AcceptLogoutToken(ctx context.Context, provider kernel.ULID, jti string, expires time.Time) (bool, error)
	// RevokeIdentitySessions revokes the live sessions signed in through
	// the provider with the provider session sid, or, when sid is empty,
	// with the subject, and returns them.
	RevokeIdentitySessions(ctx context.Context, provider kernel.ULID, sid, subject string) ([]kernel.ULID, error)
	// SessionIDToken opens the ID token the session id was signed in with,
	// and false when it holds none.
	SessionIDToken(ctx context.Context, id kernel.ULID) (crypt.Value, bool, error)
	// ExpireSignIns deletes the requests that ended, consumed or expired,
	// and the logout tokens that expired, before before.
	ExpireSignIns(ctx context.Context, before time.Time) (int64, error)
}

// SignInRecord is what a verified callback asks the store to write.
type SignInRecord struct {
	Provider Provider
	Identity Identity
	// Session is the session to create; the store sets its member and
	// actor to the member the identity resolves to.
	Session session.Session
	// IDToken is sealed beside the session for the logout's hint.
	IDToken string
	// Enable, when set, enables the provider and is its trail entry.
	Enable *kernel.OrganizationAuditEntry
	// Member, when not zero, is the member a re-authentication must
	// resolve to.
	Member kernel.MemberID
}

// SignInDeps are what the sign-in needs.
type SignInDeps struct {
	Store  SignInStore
	Party  RelyingParty
	Admins Admins
	// Features checks the provider kind's feature at each start; nil
	// checks none.
	Features featuresapi.Options
	// Drop drops a revoked session from this replica's cache at once.
	Drop func(kernel.ULID)
	// PublicURL is server.public_url: the redirect URI's base, and where
	// the provider sends the browser after its logout.
	PublicURL string
	// Idle and MaxAge are a browser session's limits.
	Idle, MaxAge time.Duration
	Now          func() time.Time
}

// SignIn signs people in to an organization through its OIDC providers:
// the authorization code flow with PKCE and a nonce, bound to the browser
// that started it, and the provider's logouts.
//
// ref: decision 0006 § "Human login"
type SignIn struct{ d SignInDeps }

// NewSignIn returns the sign-in on d.
func NewSignIn(d SignInDeps) *SignIn { return &SignIn{d: d} }

// Start is a browser's request to start a sign-in.
type Start struct {
	Organization string
	Provider     string
	Purpose      string
	ReturnTo     string
	// By is the browser's live session's principal, nil for none.
	By *kernel.Principal
}

// Started is where the browser goes, the authorization request, and the
// value of the cookie that binds the request to it until Expires.
type Started struct {
	Location string
	Cookie   string
	Expires  time.Time
}

// Callback is the provider's answer as the browser brings it back.
type Callback struct {
	State, Code, Error string
	// Cookie is the browser's RequestCookie value.
	Cookie string
	// By is the browser's live session's principal, nil for none.
	By *kernel.Principal
	IP string
}

// SignedIn is the browser session a callback created, its cookie value,
// and where the browser goes next.
type SignedIn struct {
	Secret   string
	Session  session.Session
	ReturnTo string
}

// Organization reads the organization slug names and its providers a
// browser may start a sign-in through: its enabled OIDC providers, oldest
// first. An unknown or malformed slug is port.ErrNotFound.
func (s *SignIn) Organization(ctx context.Context, slug string) (organization.Organization, []Provider, error) {
	org, err := s.organization(ctx, slug)
	if err != nil {
		return organization.Organization{}, nil, err
	}
	all, err := s.d.Store.Providers(ctx, org.ID)
	if err != nil {
		return organization.Organization{}, nil, err //nolint:wrapcheck // the store names the read
	}
	var out []Provider
	for _, p := range all {
		if p.Kind == OIDC && p.Enabled() {
			out = append(out, p)
		}
	}
	return org, out, nil
}

// SoleOrganization is the installation's only organization's slug, and
// false when it has none or several.
func (s *SignIn) SoleOrganization(ctx context.Context) (kernel.Slug, bool, error) {
	return s.d.Store.SoleOrganization(ctx) //nolint:wrapcheck // the store names the read
}

func (s *SignIn) organization(ctx context.Context, slug string) (organization.Organization, error) {
	sl, err := organization.ParseSlug("organization", slug)
	if err != nil {
		return organization.Organization{}, fmt.Errorf("%w: organization %q", port.ErrNotFound, slug)
	}
	return s.d.Store.SignInOrganization(ctx, sl) //nolint:wrapcheck // the store names the read
}

// Start starts a sign-in through an organization's OIDC provider: it
// stores the request, bound to a new browser cookie, and answers the
// authorization request with a PKCE S256 challenge, a nonce and Scopes,
// and max_age=0 with prompt=login for a fresh purpose. A sign-in needs the
// provider enabled; enabling needs the browser to hold an organization
// admin's session of the organization; re-authentication a member's. The
// kind's feature refused is a *featuresapi.Refusal.
func (s *SignIn) Start(ctx context.Context, in Start) (Started, error) {
	org, err := s.organization(ctx, in.Organization)
	if err != nil {
		return Started{}, err
	}
	p, err := s.provider(ctx, org.ID, in.Provider)
	if err != nil {
		return Started{}, err
	}
	purpose, err := ParsePurpose(in.Purpose)
	if err != nil {
		return Started{}, err
	}
	if p.Kind != OIDC || purpose == PurposeLink {
		return Started{}, fmt.Errorf("%w: a %s %s through %s", ErrNotOffered, p.Kind, purpose, p.Name)
	}
	if s.d.Features != nil {
		if err := s.d.Features.AllowAccount(ctx, org.ID, kernel.AccountID{}, p.Kind.Feature()); err != nil {
			return Started{}, err //nolint:wrapcheck // a *featuresapi.Refusal, answered as it is
		}
	}
	if err := s.allowed(ctx, org.ID, p, purpose, in.By); err != nil {
		return Started{}, err
	}
	returnTo, err := CheckReturnTo(in.ReturnTo)
	if err != nil {
		return Started{}, err
	}
	ep, err := s.d.Party.Discover(ctx, p.Settings.IdentityProviderHost)
	if err != nil {
		return Started{}, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	now := s.d.Now()
	state, cookie, nonce := random(), random(), random()
	r := Request{
		ID: kernel.NewULID(), OrganizationID: org.ID, ProviderID: p.ID, Purpose: purpose,
		StateHash: digest(state), Nonce: nonce, Verifier: oauth2.GenerateVerifier(),
		BrowserHash: digest(cookie), ReturnTo: returnTo, CreatedAt: now, ExpiresAt: now.Add(RequestTTL),
	}
	if err := s.d.Store.CreateSignInRequest(ctx, r); err != nil {
		return Started{}, err //nolint:wrapcheck // the store names the write
	}
	conf := oauth2.Config{
		ClientID:    p.Settings.ClientID,
		Endpoint:    oauth2.Endpoint{AuthURL: ep.Authorization, TokenURL: ep.Token},
		RedirectURL: s.redirectURI(),
		Scopes:      Scopes,
	}
	opts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(r.Verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	}
	if purpose.Fresh() {
		opts = append(opts, oauth2.SetAuthURLParam("max_age", "0"),
			oauth2.SetAuthURLParam("prompt", "login"))
	}
	return Started{Location: conf.AuthCodeURL(state, opts...), Cookie: cookie, Expires: r.ExpiresAt}, nil
}

// allowed refuses a start its browser may not make: a sign-in or a
// re-authentication through a disabled provider, an enable without an
// organization admin's session of org, and a re-authentication without a
// member's.
func (s *SignIn) allowed(ctx context.Context, org kernel.OrganizationID, p Provider, purpose Purpose, by *kernel.Principal) error {
	ofOrg := by != nil && by.OrganizationID == org
	switch purpose {
	case PurposeEnable:
		if !ofOrg || s.d.Admins.Admit(ctx, orgadmin.Actor{Principal: *by}) != nil {
			return fmt.Errorf("%w: enabling %s needs an organization admin's session", ErrForbidden, p.Name)
		}
		return nil
	case PurposeReauthenticate:
		if !ofOrg || by.Kind != kernel.PrincipalUser || by.MemberID.IsZero() {
			return fmt.Errorf("%w: re-authentication needs a member's session", ErrForbidden)
		}
	}
	if !p.Enabled() {
		return fmt.Errorf("%w: %s is not enabled", ErrNotOffered, p.Name)
	}
	return nil
}

// provider reads org's provider id names; an ID that cannot be one is
// not found.
func (s *SignIn) provider(ctx context.Context, org kernel.OrganizationID, id string) (Provider, error) {
	u, err := kernel.ParseULID(id)
	if err != nil {
		return Provider{}, fmt.Errorf("%w: identity provider %q", port.ErrNotFound, id)
	}
	return s.d.Store.Provider(ctx, org, u) //nolint:wrapcheck // the store names the read
}

// redirectURI is every OIDC provider's redirect URI.
func (s *SignIn) redirectURI() string {
	return strings.TrimSuffix(s.d.PublicURL, "/") + CallbackPath
}

// Callback finishes the sign-in the state names, once, for the browser
// that started it: it redeems the code, verifies the ID token and its
// claims, and signs the identity in as a browser session of actor kind
// user in no account. An enabling sign-in enables the provider with the
// login as its admin login. Every refusal leaves no session.
func (s *SignIn) Callback(ctx context.Context, in Callback) (SignedIn, error) {
	if in.State == "" || in.Cookie == "" {
		return SignedIn{}, ErrRequest
	}
	r, err := s.d.Store.ConsumeSignInRequest(ctx, digest(in.State), digest(in.Cookie))
	if errors.Is(err, port.ErrNotFound) {
		return SignedIn{}, ErrRequest
	}
	if err != nil {
		return SignedIn{}, err //nolint:wrapcheck // the store names the write
	}
	p, err := s.d.Store.Provider(ctx, r.OrganizationID, r.ProviderID)
	if errors.Is(err, port.ErrNotFound) {
		return SignedIn{}, fmt.Errorf("%w: the provider is gone", ErrNotOffered)
	}
	if err != nil {
		return SignedIn{}, err //nolint:wrapcheck // the store names the read
	}
	if in.Error != "" {
		return SignedIn{}, fmt.Errorf("%w: %s answered %q", ErrProvider, p.Name, in.Error)
	}
	raw, claims, err := s.redeem(ctx, p, r, in.Code)
	if err != nil {
		return SignedIn{}, err
	}
	if err := CheckIDToken(claims, r, p.Settings.ClientID, s.d.Now()); err != nil {
		return SignedIn{}, err
	}
	id, err := p.Settings.Identity(claims)
	if err != nil {
		return SignedIn{}, err
	}
	rec := SignInRecord{Provider: p, Identity: id, IDToken: raw}
	switch r.Purpose {
	case PurposeEnable:
		// The store names the member the sign-in resolves to as the actor.
		e := entry(orgadmin.Actor{Principal: kernel.Principal{
			Kind: kernel.PrincipalUser, OrganizationID: p.OrganizationID,
		}, RemoteIP: in.IP}, ActionEnable, "UPDATE", p.ID, body(map[string]any{"adminLogin": id.Login}), s.d.Now())
		rec.Enable = &e
	case PurposeReauthenticate:
		if in.By == nil || in.By.MemberID.IsZero() || in.By.OrganizationID != p.OrganizationID {
			return SignedIn{}, fmt.Errorf("%w: re-authentication needs a member's session", ErrForbidden)
		}
		rec.Member = in.By.MemberID
	default:
		if !p.Enabled() {
			return SignedIn{}, fmt.Errorf("%w: %s is not enabled", ErrNotOffered, p.Name)
		}
	}
	secret, ss := s.session(p, id, in.IP)
	rec.Session = ss
	out, err := s.d.Store.SignInIdentity(ctx, rec)
	if err != nil {
		return SignedIn{}, err //nolint:wrapcheck // the store names the write and its refusals
	}
	return SignedIn{Secret: secret, Session: out, ReturnTo: r.ReturnTo}, nil
}

// redeem exchanges the code for the ID token and verifies it.
func (s *SignIn) redeem(ctx context.Context, p Provider, r Request, code string) (string, map[string]any, error) {
	ep, err := s.d.Party.Discover(ctx, p.Settings.IdentityProviderHost)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	secret, err := s.d.Store.ProviderSecret(ctx, p.OrganizationID, p.ID)
	if err != nil {
		return "", nil, err //nolint:wrapcheck // the store names the read
	}
	raw, err := s.d.Party.Exchange(ctx, ep, Client{
		ID: p.Settings.ClientID, Secret: string(secret.Reveal()), RedirectURI: s.redirectURI(),
	}, code, r.Verifier)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	claims, err := s.d.Party.VerifyIDToken(ctx, ep.Issuer, p.Settings.ClientID, raw)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	return raw, claims, nil
}

// session is the browser session a sign-in of id through p creates: a WEB
// session of actor kind user in no account, whose original teams are the
// identity's groups, and its cookie value.
func (s *SignIn) session(p Provider, id Identity, ip string) (string, session.Session) {
	now := s.d.Now()
	secret, hash := session.NewSecret()
	member, subject := true, id.Subject
	return secret, session.Session{
		ID:                 kernel.NewULID(),
		OrganizationID:     p.OrganizationID,
		Channel:            session.ChannelWeb,
		ActorKind:          kernel.PrincipalUser,
		Login:              id.Login,
		Name:               id.Name,
		Email:              id.Email,
		Teams:              id.Groups,
		OriginalMember:     &member,
		OriginalTeamsKnown: true,
		OriginalTeams:      id.Groups,
		IdentityProviderID: p.ID,
		IDPSubject:         &subject,
		IDPSID:             id.SID,
		IPAddress:          ip,
		CreatedAt:          now,
		ExpiresAt:          now.Add(s.d.MaxAge),
		IdleExpiresAt:      now.Add(s.d.Idle),
		SecretHash:         hash,
	}
}

// LogoutURL is where a browser ends ss at the provider it signed in
// through: the provider's end_session_endpoint with the ID token as
// id_token_hint, the client ID, and the public URL as
// post_logout_redirect_uri. It is empty for a session of no provider, a
// provider that advertises no endpoint, and a provider that cannot be
// read, since the session has ended at infraweave whatever the provider
// answers.
func (s *SignIn) LogoutURL(ctx context.Context, ss session.Session) string {
	if ss.IdentityProviderID.IsZero() {
		return ""
	}
	p, err := s.d.Store.Provider(ctx, ss.OrganizationID, ss.IdentityProviderID)
	if err != nil || p.Kind != OIDC {
		return ""
	}
	ep, err := s.d.Party.Discover(ctx, p.Settings.IdentityProviderHost)
	if err != nil || ep.EndSession == "" {
		return ""
	}
	u, err := url.Parse(ep.EndSession)
	if err != nil {
		return ""
	}
	q := u.Query()
	if tok, ok, err := s.d.Store.SessionIDToken(ctx, ss.ID); err == nil && ok {
		q.Set("id_token_hint", string(tok.Reveal()))
	}
	q.Set("client_id", p.Settings.ClientID)
	q.Set("post_logout_redirect_uri", s.d.PublicURL)
	u.RawQuery = q.Encode()
	return u.String()
}

// ErrLogoutToken refuses a back-channel logout token: unverifiable by any
// provider it names, or replayed.
var ErrLogoutToken = errors.New("logout token refused")

// BackchannelLogout ends the sessions a provider's back-channel logout
// token names: those signed in through the provider with its sid, or with
// its subject when it names no sid. The providers tried are those whose
// client ID the token's aud names and whose issuer is its iss; one of
// them must verify it with VerifyLogout. Each jti is accepted once.
//
// ref: decision 0006 § "Human login"
func (s *SignIn) BackchannelLogout(ctx context.Context, raw string) error {
	iss, aud, ok := peek(raw)
	if !ok {
		return fmt.Errorf("%w: not a JWT naming iss and aud", ErrLogoutToken)
	}
	ps, err := s.d.Store.ProvidersByClientID(ctx, aud)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	for _, p := range ps {
		ep, err := s.d.Party.Discover(ctx, p.Settings.IdentityProviderHost)
		if err != nil || ep.Issuer != iss {
			continue
		}
		tok, err := s.d.Party.VerifyLogout(ctx, ep.Issuer, p.Settings.ClientID, raw)
		if err != nil {
			continue
		}
		fresh, err := s.d.Store.AcceptLogoutToken(ctx, p.ID, tok.ID, tok.Expiry.Add(Skew))
		if err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		if !fresh {
			return fmt.Errorf("%w: jti %q was accepted already", ErrLogoutToken, tok.ID)
		}
		ids, err := s.d.Store.RevokeIdentitySessions(ctx, p.ID, tok.SID, tok.Subject)
		if err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		for _, id := range ids {
			s.d.Drop(id)
		}
		return nil
	}
	return fmt.Errorf("%w: no provider verifies it", ErrLogoutToken)
}

// Expire is authn.sessions.expire's part of the sign-in: it deletes the
// requests and logout tokens that have ended.
func (s *SignIn) Expire(ctx context.Context) error {
	_, err := s.d.Store.ExpireSignIns(ctx, s.d.Now())
	return err //nolint:wrapcheck // the store names the write
}

// random is 32 random bytes in unpadded base64url: a state, a nonce or a
// cookie value.
func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand never fails
	return base64.RawURLEncoding.EncodeToString(b)
}

// digest is the SHA-256 a state or a cookie value is stored as.
func digest(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}
