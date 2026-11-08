package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrUnauthenticated is every refused credential, whichever part failed,
// so the answer tells a caller nothing it could probe with.
//
// ref: decision 0006
var ErrUnauthenticated = errors.New("unauthenticated")

// Retention is how long a session's row outlives its end before
// authn.sessions.expire deletes it, so the session list and audit can
// still name it.
const Retention = 30 * 24 * time.Hour

// OperatorSubject prefixes an operator token's sub, operator::<key ID>.
const OperatorSubject = "operator::"

// APIKeySubject prefixes an API token's sub, api::<key ID>.
const APIKeySubject = "api::"

// OrganizationKeySubject prefixes an organization token's sub,
// org::<key ID>.
const OrganizationKeySubject = "org::"

// OIDCKeySubject prefixes the sub of an OIDC API key's token,
// api::oidc::<client ID>::<key ID>.
const OIDCKeySubject = APIKeySubject + "oidc::"

// Tokens issues and verifies API tokens; *token.Service is one.
type Tokens interface {
	Issue(ctx context.Context, r token.Request) (string, token.Claims, error)
	Verify(ctx context.Context, raw, audience string) (token.Claims, error)
}

// Deps are what the service needs.
type Deps struct {
	Keys port.APIKeys
	// OrganizationKeys reads organization keys for the exchange; nil
	// refuses every one.
	OrganizationKeys port.OrganizationKeys
	Sessions         port.Sessions
	Tokens           Tokens
	// OIDC verifies an OIDC API key's JWT; nil, or a nil IssuerAllowed,
	// refuses every OIDC key.
	OIDC port.OIDC
	// IssuerAllowed reports whether an OIDC key's issuer may still be
	// fetched, as auth.oidc_api_keys.allowed_issuers says at the exchange.
	IssuerAllowed func(issuer string) bool
	// Audience is every API token's aud, server.public_url/api/v1.
	Audience string
	// Operators are auth.operator.keys: each operator key's SHA-256 of its
	// secret by the key's ID.
	Operators map[kernel.ULID][sha256.Size]byte
	// OperatorAudience is every operator token's aud,
	// server.public_url/operator/v1, which no account route accepts.
	OperatorAudience string
	// TTL is auth.tokens.api.ttl, an API token's longest life.
	TTL time.Duration
	// BreakGlass reads break-glass credentials and records sign-ins.
	BreakGlass port.BreakGlass
	// Admission decides which accounts admit a member.
	Admission session.Admission
	// AccountAdmin decides whether an account session's actor is an
	// account admin, which the account's bindings say and no session
	// stores; nil keeps the session's stored Admin.
	AccountAdmin func(ctx context.Context, p kernel.Principal) (bool, error)
	// Idle and MaxAge are auth.session's idle_timeout and max_age, a
	// browser session's limits.
	Idle, MaxAge time.Duration
	Now          func() time.Time
}

// Service exchanges API keys for tokens, signs people in, and
// authenticates both.
type Service struct {
	d       Deps
	cache   *session.Cache
	secrets *secretIndex
}

// New returns the service on d, with this replica's session cache.
func New(d Deps) *Service {
	return &Service{
		d:       d,
		cache:   session.NewCache(d.Sessions.Session, d.Now),
		secrets: &secretIndex{ids: map[string]kernel.ULID{}},
	}
}

// Cache is the replica's session cache, which the invalidate watcher
// drops sessions from.
func (s *Service) Cache() *session.Cache { return s.cache }

// Grant is an issued token and the session it stands for.
type Grant struct {
	Token   string
	Claims  token.Claims
	Session session.Session
}

// Exchange trades an API key, an organization key or an operator key for
// a token and the session it stands for, recording ip as the session's
// address. An operator key's token is an operator token
// (exchangeOperator), and an iwo_ secret's an organization token
// (exchangeOrganization). A secret key's secret is its iwk_ secret; an OIDC key's is a JWT of its issuer, whose sub must
// match the key's subject expression and whose mapped claims add teams.
// A personal key's session acts as its owner in the key's account, with
// the owner's login, name and teams, and only while the account admits
// the owner as an active member, whatever the key's own state says.
// The token lives auth.tokens.api.ttl, never past the key's expiry. A key
// that does not exist, whose secret fails, or that is expired, disabled
// or deleted is ErrUnauthenticated.
func (s *Service) Exchange(ctx context.Context, id, secret, ip string) (Grant, error) {
	keyID, err := kernel.ParseULID(id)
	if err != nil {
		return Grant{}, ErrUnauthenticated
	}
	if digest, ok := s.d.Operators[keyID]; ok {
		return s.exchangeOperator(ctx, keyID, digest, secret, ip)
	}
	if strings.HasPrefix(secret, apikey.OrganizationPrefix) {
		return s.exchangeOrganization(ctx, keyID, secret, ip)
	}
	k, err := s.d.Keys.APIKey(ctx, keyID)
	if errors.Is(err, port.ErrNotFound) {
		// Hashing anyway keeps an unknown ID as slow as a wrong secret.
		apikey.Key{}.Matches(secret)
		return Grant{}, ErrUnauthenticated
	}
	if err != nil {
		return Grant{}, err //nolint:wrapcheck // the store names the read
	}
	now := s.d.Now()
	subject, teams := APIKeySubject+k.ID.String(), k.Teams
	switch {
	case k.Type == apikey.TypeOIDC:
		var ok bool
		if teams, ok = s.oidc(ctx, k, secret); !ok {
			return Grant{}, ErrUnauthenticated
		}
		subject = OIDCKeySubject + k.OIDC.ClientID + "::" + k.ID.String()
	case !k.Matches(secret):
		return Grant{}, ErrUnauthenticated
	}
	if !k.Usable(now) {
		return Grant{}, ErrUnauthenticated
	}
	login, name := k.ID.String(), k.Name
	if k.Personal() {
		o, err := s.d.Keys.KeyOwner(ctx, k.AccountID, k.Owner)
		if errors.Is(err, port.ErrNotFound) {
			return Grant{}, ErrUnauthenticated
		}
		if err != nil {
			return Grant{}, err //nolint:wrapcheck // the store names the read
		}
		login, name, teams = o.Login, o.Name, o.Teams
	}
	ttl, ok := s.ttl(k.ExpiresAt, now)
	if !ok {
		return Grant{}, ErrUnauthenticated
	}
	if err := s.d.Keys.TouchAPIKey(ctx, k.ID); err != nil {
		return Grant{}, err //nolint:wrapcheck // the store names the write
	}
	sid := kernel.NewULID()
	raw, claims, err := s.d.Tokens.Issue(ctx, token.Request{
		Kind:     token.KindAPIKey,
		Subject:  subject,
		Session:  sid,
		Audience: s.d.Audience,
		TTL:      ttl,
	})
	if err != nil {
		return Grant{}, fmt.Errorf("exchange API key %s: %w", k.ID, err)
	}
	ss := session.Session{
		ID:             sid,
		OrganizationID: k.OrganizationID,
		AccountID:      k.AccountID,
		Channel:        session.ChannelUnknown,
		ActorKind:      kernel.PrincipalAPIKey,
		ActorID:        k.ID,
		MemberID:       k.Owner,
		Login:          login,
		Name:           name,
		Teams:          teams,
		IPAddress:      ip,
		CreatedAt:      now,
		ExpiresAt:      claims.Expiry,
	}
	// The token is unusable until its session exists, so a failure here
	// leaves nothing that authenticates.
	if err := s.d.Sessions.CreateSession(ctx, ss); err != nil {
		return Grant{}, err //nolint:wrapcheck // the store names the write
	}
	return Grant{Token: raw, Claims: claims, Session: ss}, nil
}

// exchangeOrganization trades the organization key id for an organization
// token: a session of actor kind organization in no account, which only
// the Organization tag's routes accept. Its use takes no seat.
func (s *Service) exchangeOrganization(
	ctx context.Context,
	id kernel.ULID,
	secret, ip string,
) (Grant, error) {
	if s.d.OrganizationKeys == nil {
		return Grant{}, ErrUnauthenticated
	}
	k, err := s.d.OrganizationKeys.OrganizationKey(ctx, id)
	if errors.Is(err, port.ErrNotFound) {
		// Hashing anyway keeps an unknown ID as slow as a wrong secret.
		apikey.OrganizationKey{}.Matches(secret)
		return Grant{}, ErrUnauthenticated
	}
	if err != nil {
		return Grant{}, err //nolint:wrapcheck // the store names the read
	}
	now := s.d.Now()
	if !k.Matches(secret) || !k.Usable(now) {
		return Grant{}, ErrUnauthenticated
	}
	ttl, ok := s.ttl(k.ExpiresAt, now)
	if !ok {
		return Grant{}, ErrUnauthenticated
	}
	if err := s.d.OrganizationKeys.TouchOrganizationKey(ctx, k.ID); err != nil {
		return Grant{}, err //nolint:wrapcheck // the store names the write
	}
	sid := kernel.NewULID()
	raw, claims, err := s.d.Tokens.Issue(ctx, token.Request{
		Kind:     token.KindOrganizationKey,
		Subject:  OrganizationKeySubject + k.ID.String(),
		Session:  sid,
		Audience: s.d.Audience,
		TTL:      ttl,
	})
	if err != nil {
		return Grant{}, fmt.Errorf("exchange organization key %s: %w", k.ID, err)
	}
	ss := session.Session{
		ID:             sid,
		OrganizationID: k.OrganizationID,
		Channel:        session.ChannelUnknown,
		ActorKind:      kernel.PrincipalOrganization,
		ActorID:        k.ID,
		Login:          k.ID.String(),
		Name:           k.Name,
		IPAddress:      ip,
		CreatedAt:      now,
		ExpiresAt:      claims.Expiry,
	}
	if err := s.d.Sessions.CreateSession(ctx, ss); err != nil {
		return Grant{}, err //nolint:wrapcheck // the store names the write
	}
	return Grant{Token: raw, Claims: claims, Session: ss}, nil
}

// ttl is a key's token's life at now: auth.tokens.api.ttl, never past
// expires unless it is zero, in whole seconds, since Issue counts exp from
// now in whole seconds and so keeps exp at or before the key's expiry.
// False when less than a second is left.
func (s *Service) ttl(expires, now time.Time) (time.Duration, bool) {
	ttl := s.d.TTL
	if !expires.IsZero() {
		ttl = min(ttl, expires.Sub(now))
	}
	ttl = ttl.Truncate(time.Second)
	return ttl, ttl >= time.Second
}

// exchangeOperator trades the operator key id, whose secret's SHA-256 is
// digest, for an operator token: its aud is the operator API's, so only
// /operator/v1 accepts it, and its session belongs to no organization.
func (s *Service) exchangeOperator(
	ctx context.Context,
	id kernel.ULID,
	digest [sha256.Size]byte,
	secret, ip string,
) (Grant, error) {
	sum := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(sum[:], digest[:]) != 1 || s.d.OperatorAudience == "" {
		return Grant{}, ErrUnauthenticated
	}
	sid := kernel.NewULID()
	raw, claims, err := s.d.Tokens.Issue(ctx, token.Request{
		Kind:     token.KindOperator,
		Subject:  OperatorSubject + id.String(),
		Session:  sid,
		Audience: s.d.OperatorAudience,
		TTL:      s.d.TTL.Truncate(time.Second),
	})
	if err != nil {
		return Grant{}, fmt.Errorf("exchange operator key %s: %w", id, err)
	}
	ss := session.Session{
		ID:        sid,
		Channel:   session.ChannelUnknown,
		ActorKind: kernel.PrincipalOperator,
		ActorID:   id,
		Login:     id.String(),
		Name:      "operator",
		IPAddress: ip,
		CreatedAt: s.d.Now(),
		ExpiresAt: claims.Expiry,
	}
	if err := s.d.Sessions.CreateSession(ctx, ss); err != nil {
		return Grant{}, err //nolint:wrapcheck // the store names the write
	}
	return Grant{Token: raw, Claims: claims, Session: ss}, nil
}

// oidc verifies raw for the OIDC key k and returns the session's teams:
// the issuer must still be allowed, the token must verify for the key's
// client ID, and its sub must match the key's anchored subject
// expression.
func (s *Service) oidc(ctx context.Context, k apikey.Key, raw string) ([]string, bool) {
	if s.d.OIDC == nil || s.d.IssuerAllowed == nil || !s.d.IssuerAllowed(k.OIDC.Issuer) {
		return nil, false
	}
	claims, err := s.d.OIDC.Verify(ctx, k.OIDC.Issuer, k.OIDC.ClientID, raw)
	if err != nil {
		return nil, false
	}
	sub, _ := claims["sub"].(string)
	re, err := apikey.CompileSubject(k.OIDC.SubjectExpression)
	if err != nil || sub == "" || !re.MatchString(sub) {
		return nil, false
	}
	return k.OIDC.Teams(k.Teams, claims), true
}

// actors are the principal kind each token kind's session must have.
var actors = map[token.Kind]kernel.PrincipalKind{
	token.KindUser:            kernel.PrincipalUser,
	token.KindAPIKey:          kernel.PrincipalAPIKey,
	token.KindRun:             kernel.PrincipalRun,
	token.KindOrganizationKey: kernel.PrincipalOrganization,
	token.KindOperator:        kernel.PrincipalOperator,
	token.KindOAuth:           kernel.PrincipalUser,
}

// Reach is which sessions a route admits (accessapi.Access).
type Reach int

const (
	// ReachAccount admits a session that works in an account.
	ReachAccount Reach = iota
	// ReachOrganization also admits a session of an organization that
	// works in none, such as the break-glass admin or a member no account
	// admits, but no organization token's.
	ReachOrganization
	// ReachOrganizationAdmin admits the sessions that may hold the
	// organization role, a member's and the break-glass admin's, whether
	// they work in an account or not, and organization tokens'. The
	// Organization tag's service refuses a member without the role.
	ReachOrganizationAdmin
)

// Authenticate is the principal an API token acts as on a route of reach,
// and the token's claims. A token that fails verification, names no
// session or one that is not live, or whose session's actor is not of the
// token's kind or does not reach the route, is ErrUnauthenticated, and so
// is an operator principal, which belongs to no organization. A store
// that cannot be read is not.
func (s *Service) Authenticate(
	ctx context.Context,
	raw string,
	reach Reach,
) (kernel.Principal, token.Claims, error) {
	return s.authenticate(ctx, raw, s.d.Audience, reaches(reach))
}

// reaches is which sessions a route of reach admits.
func reaches(reach Reach) func(session.Session) bool {
	return func(ss session.Session) bool {
		switch reach {
		case ReachOrganization:
			return !ss.OrganizationID.IsZero() && ss.ActorKind != kernel.PrincipalOrganization
		case ReachOrganizationAdmin:
			switch ss.ActorKind {
			case kernel.PrincipalUser, kernel.PrincipalBreakGlass, kernel.PrincipalOrganization:
				return !ss.OrganizationID.IsZero()
			}
			return false
		}
		return !ss.AccountID.IsZero()
	}
}

// AuthenticateOperator is the operator principal an operator token acts as
// on /operator/v1, and the token's claims; any other token, an API
// token among them, is ErrUnauthenticated, as Authenticate's are.
func (s *Service) AuthenticateOperator(
	ctx context.Context,
	raw string,
) (kernel.Principal, token.Claims, error) {
	if s.d.OperatorAudience == "" {
		return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
	}
	return s.authenticate(ctx, raw, s.d.OperatorAudience, func(ss session.Session) bool {
		return ss.ActorKind == kernel.PrincipalOperator
	})
}

// AuthenticateGrant is the principal an OAuth access token acts as on the
// MCP surface resource, the URL the token's aud must be, and the token's
// claims: the member in the grant's account, whose Scopes are the
// grant's. A token of another kind or resource, or whose grant is not
// live, is ErrUnauthenticated; so is a token whose scope the grant no
// longer holds.
func (s *Service) AuthenticateGrant(
	ctx context.Context,
	raw, resource string,
) (kernel.Principal, token.Claims, error) {
	p, c, err := s.authenticate(ctx, raw, resource, func(ss session.Session) bool {
		return ss.ClientID != "" && ss.Resource == resource && !ss.AccountID.IsZero()
	})
	if err != nil {
		return kernel.Principal{}, token.Claims{}, err
	}
	if c.Kind != token.KindOAuth {
		return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
	}
	for sc := range strings.FieldsSeq(c.Scope) {
		if !slices.Contains(p.Scopes, sc) {
			return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
		}
	}
	p.Scopes = strings.Fields(c.Scope)
	return p, c, nil
}

// authenticate verifies raw for audience and reads its session, which must
// be live, of the token's kind's actor, and one route accepts.
func (s *Service) authenticate(
	ctx context.Context,
	raw, audience string,
	route func(session.Session) bool,
) (kernel.Principal, token.Claims, error) {
	c, err := s.d.Tokens.Verify(ctx, raw, audience)
	if errors.Is(err, token.ErrToken) {
		return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
	}
	if err != nil {
		return kernel.Principal{}, token.Claims{}, err //nolint:wrapcheck // the store names the read
	}
	ss, err := s.cache.Get(ctx, c.Session)
	if errors.Is(err, port.ErrNotFound) {
		return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
	}
	if err != nil {
		return kernel.Principal{}, token.Claims{}, err //nolint:wrapcheck // as above
	}
	if !ss.Live(s.d.Now()) || actors[c.Kind] != ss.ActorKind || !route(ss) {
		return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
	}
	p, err := s.principal(ctx, ss)
	return p, c, err
}

// principal is ss's principal, whose Admin, for a session of an account,
// is decided from the account's bindings at this request rather than at
// sign-in, so a binding's change applies to live sessions.
func (s *Service) principal(ctx context.Context, ss session.Session) (kernel.Principal, error) {
	p := ss.Principal()
	if p.AccountID.IsZero() || s.d.AccountAdmin == nil {
		return p, nil
	}
	admin, err := s.d.AccountAdmin(ctx, p)
	if err != nil {
		return kernel.Principal{}, fmt.Errorf("decide account admin: %w", err)
	}
	p.Admin = admin
	return p, nil
}

// Sessions are up to limit of the live sessions of p's account, from the
// first after after.
func (s *Service) Sessions(
	ctx context.Context,
	p kernel.Principal,
	after kernel.ULID,
	limit int,
) ([]session.Session, error) {
	return s.d.Sessions.Sessions(ctx, p.AccountID, after, limit) //nolint:wrapcheck // as above
}

// Revoke revokes the session id of p's account and returns it; one of
// another account is port.ErrNotFound. This replica's cache drops it at
// once, the others on its message.
func (s *Service) Revoke(
	ctx context.Context,
	p kernel.Principal,
	id kernel.ULID,
) (session.Session, error) {
	ss, err := s.d.Sessions.RevokeSession(ctx, p.AccountID, id)
	if err != nil {
		return session.Session{}, err //nolint:wrapcheck // as above
	}
	s.cache.Drop(id)
	return ss, nil
}

// RevokeOthers revokes every live session of p's account but p's own, and
// returns them.
func (s *Service) RevokeOthers(ctx context.Context, p kernel.Principal) ([]session.Session, error) {
	out, err := s.d.Sessions.RevokeSessionsExcept(ctx, p.AccountID, p.SessionID)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	for _, ss := range out {
		s.cache.Drop(ss.ID)
	}
	return out, nil
}

// Logout ends p's own session, which may work in no account, and returns
// it. A CLI session it parents lives on.
func (s *Service) Logout(ctx context.Context, p kernel.Principal) (session.Session, error) {
	ss, err := s.d.Sessions.EndSession(ctx, p.SessionID)
	if err != nil {
		return session.Session{}, err //nolint:wrapcheck // as above
	}
	s.cache.Drop(ss.ID)
	return ss, nil
}

// Expire is authn.sessions.expire: it deletes the sessions that ended
// more than Retention ago.
func (s *Service) Expire(ctx context.Context) error {
	_, err := s.d.Sessions.ExpireSessions(ctx, s.d.Now().Add(-Retention))
	return err //nolint:wrapcheck // as above
}
