package port

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrNotFound reports a row that does not exist.
var ErrNotFound = errors.New("not found")

// ErrSlugTaken reports a slug another organization, or another account of
// any organization, already holds; the error names which.
var ErrSlugTaken = errors.New("slug taken")

// Tenancy reads organizations and accounts; operator.Store writes them.
type Tenancy interface {
	Organization(ctx context.Context, id kernel.OrganizationID) (organization.Organization, error)
	Account(ctx context.Context, id kernel.AccountID) (account.Account, error)
}

// Sessions stores sessions. Every revocation sends session.Key on
// infraweave_invalidate in its own transaction, so no replica's cache
// outlives the commit by more than the message's delivery.
type Sessions interface {
	// CreateSession records s. A member's session in any channel but
	// UNKNOWN first revokes the member's live sessions in that channel.
	CreateSession(ctx context.Context, s session.Session) error
	Session(ctx context.Context, id kernel.ULID) (session.Session, error)
	// Sessions are up to limit of the account's sessions that are neither
	// revoked nor past their end, in ID order, which is creation order,
	// from the first after after; a zero after starts at the first.
	Sessions(
		ctx context.Context,
		acct kernel.AccountID,
		after kernel.ULID,
		limit int,
	) ([]session.Session, error)
	// RevokeSession revokes the account's session id and returns it; a
	// session of another account is ErrNotFound.
	RevokeSession(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
	) (session.Session, error)
	// RevokeSessionsExcept revokes every live session of the account but
	// except, and returns them.
	RevokeSessionsExcept(
		ctx context.Context,
		acct kernel.AccountID,
		except kernel.ULID,
	) ([]session.Session, error)
	// ExpireSessions deletes the sessions that ended before before, and
	// reports how many.
	ExpireSessions(ctx context.Context, before time.Time) (int64, error)
	// SessionBySecret reads the browser session whose cookie secret
	// hashes to hash.
	SessionBySecret(ctx context.Context, hash []byte) (session.Session, error)
	// TouchSession records a use of the session id and moves its idle end
	// to idle.
	TouchSession(ctx context.Context, id kernel.ULID, idle time.Time) error
	// EndSession revokes the session id, whatever its account or none.
	EndSession(ctx context.Context, id kernel.ULID) (session.Session, error)
}

// BreakGlass reads break-glass credentials and records sign-ins on the
// organization's trail.
type BreakGlass interface {
	// BreakGlassCredential reads the organization slug names and its
	// credential, nil when it has none.
	BreakGlassCredential(
		ctx context.Context,
		slug kernel.Slug,
	) (organization.Organization, *breakglass.Credential, error)
	// SignIn records s, a break-glass session, and e, its trail entry, in
	// one transaction.
	SignIn(ctx context.Context, s session.Session, e kernel.OrganizationAuditEntry) error
	// RefuseSignIn records e, a refused sign-in's trail entry.
	RefuseSignIn(ctx context.Context, e kernel.OrganizationAuditEntry) error
}

// OIDC verifies a JWT that an OIDC API key's caller presents.
type OIDC interface {
	// Verify checks raw's signature against the keys issuer publishes,
	// and that its iss is issuer, its aud contains audience, and its exp,
	// which it must have, and nbf hold. It returns the token's claims. A
	// token that fails any check, or an issuer that cannot be read, is an
	// error.
	Verify(ctx context.Context, issuer, audience, raw string) (map[string]any, error)
}

// APIKeys reads API keys for the exchange.
type APIKeys interface {
	// APIKey reads the key id, whatever its account, as the exchange must
	// before it knows the account.
	APIKey(ctx context.Context, id kernel.ULID) (apikey.Key, error)
	// TouchAPIKey records a use of the key id, at most once a minute. A
	// use that takes the key a seat may be refused with a
	// *featuresapi.LimitError, and the exchange then issues no token.
	TouchAPIKey(ctx context.Context, id kernel.ULID) error
}

// OrganizationKeys reads organization keys for the exchange.
type OrganizationKeys interface {
	// OrganizationKey reads the key id, whatever its organization.
	OrganizationKey(ctx context.Context, id kernel.ULID) (apikey.OrganizationKey, error)
	// TouchOrganizationKey records a use of the key id, at most once a
	// minute.
	TouchOrganizationKey(ctx context.Context, id kernel.ULID) error
}
