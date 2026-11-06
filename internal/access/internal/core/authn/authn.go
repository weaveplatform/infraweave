package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrUnauthenticated is every refused credential, whichever part failed,
// so the answer tells a caller nothing it could probe with (decision 0006).
var ErrUnauthenticated = errors.New("unauthenticated")

// Retention is how long a session's row outlives its end before
// authn.sessions.expire deletes it, so the session list and audit can
// still name it.
const Retention = 30 * 24 * time.Hour

// APIKeySubject prefixes an API token's sub, api::<key ID>.
const APIKeySubject = "api::"

// Tokens issues and verifies API tokens; *token.Service is one.
type Tokens interface {
	Issue(ctx context.Context, r token.Request) (string, token.Claims, error)
	Verify(ctx context.Context, raw, audience string) (token.Claims, error)
}

// Deps are what the service needs.
type Deps struct {
	Keys     port.APIKeys
	Sessions port.Sessions
	Tokens   Tokens
	// Audience is every API token's aud, server.public_url/api/v1.
	Audience string
	// TTL is auth.tokens.api.ttl, an API token's longest life.
	TTL time.Duration
	Now func() time.Time
}

// Service exchanges API keys for tokens and authenticates the tokens.
type Service struct {
	d     Deps
	cache *session.Cache
}

// New returns the service on d, with this replica's session cache.
func New(d Deps) *Service {
	return &Service{d: d, cache: session.NewCache(d.Sessions.Session, d.Now)}
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

// Exchange trades a secret API key for an API token and the session it
// stands for, recording ip as the session's address. The token lives
// auth.tokens.api.ttl, never past the key's expiry. A key that does not
// exist, is not a secret key, does not match secret, or is expired,
// disabled or deleted is ErrUnauthenticated.
func (s *Service) Exchange(ctx context.Context, id, secret, ip string) (Grant, error) {
	keyID, err := kernel.ParseULID(id)
	if err != nil {
		return Grant{}, ErrUnauthenticated
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
	if !k.Matches(secret) || !k.Usable(now) {
		return Grant{}, ErrUnauthenticated
	}
	ttl := s.d.TTL
	if !k.ExpiresAt.IsZero() {
		ttl = min(ttl, k.ExpiresAt.Sub(now))
	}
	// Issue counts exp from now in whole seconds; a whole-second TTL keeps
	// exp at or before the key's expiry.
	ttl = ttl.Truncate(time.Second)
	if ttl < time.Second {
		return Grant{}, ErrUnauthenticated
	}
	if err := s.d.Keys.TouchAPIKey(ctx, k.ID); err != nil {
		return Grant{}, err //nolint:wrapcheck // the store names the write
	}
	sid := kernel.NewULID()
	raw, claims, err := s.d.Tokens.Issue(ctx, token.Request{
		Kind:     token.KindAPIKey,
		Subject:  APIKeySubject + k.ID.String(),
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
		Login:          k.ID.String(),
		Name:           k.Name,
		Teams:          k.Teams,
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

// actors are the principal kind each token kind's session must have.
var actors = map[token.Kind]kernel.PrincipalKind{
	token.KindUser:            kernel.PrincipalUser,
	token.KindAPIKey:          kernel.PrincipalAPIKey,
	token.KindRun:             kernel.PrincipalRun,
	token.KindOrganizationKey: kernel.PrincipalOrganization,
}

// Authenticate is the principal an API token acts as on an account route,
// and the token's claims. A token that fails verification, names no session or one that is not
// live, or whose session's actor is not of the token's kind, is
// ErrUnauthenticated; so is a principal that works in no account, which an
// organization, break-glass or operator principal never does. A store
// that cannot be read is not.
func (s *Service) Authenticate(
	ctx context.Context,
	raw string,
) (kernel.Principal, token.Claims, error) {
	c, err := s.d.Tokens.Verify(ctx, raw, s.d.Audience)
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
	if !ss.Live(s.d.Now()) || actors[c.Kind] != ss.ActorKind || ss.AccountID.IsZero() {
		return kernel.Principal{}, token.Claims{}, ErrUnauthenticated
	}
	return ss.Principal(), c, nil
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

// Logout revokes p's own session and returns it.
func (s *Service) Logout(ctx context.Context, p kernel.Principal) (session.Session, error) {
	return s.Revoke(ctx, p, p.SessionID)
}

// Expire is authn.sessions.expire: it deletes the sessions that ended
// more than Retention ago.
func (s *Service) Expire(ctx context.Context) error {
	_, err := s.d.Sessions.ExpireSessions(ctx, s.d.Now().Add(-Retention))
	return err //nolint:wrapcheck // as above
}
