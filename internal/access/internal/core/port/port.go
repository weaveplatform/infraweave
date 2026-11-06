package port

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrNotFound reports a row that does not exist.
var ErrNotFound = errors.New("not found")

// ErrSlugTaken reports a slug another organization, or another account of
// any organization, already holds; the error names which.
var ErrSlugTaken = errors.New("slug taken")

// Tenancy stores organizations and accounts.
type Tenancy interface {
	// CreateOrganization records org and its first account in one
	// transaction, or neither.
	CreateOrganization(
		ctx context.Context,
		org organization.Organization,
		first account.Account,
	) error
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
}

// APIKeys reads API keys for the exchange.
type APIKeys interface {
	// APIKey reads the key id, whatever its account, as the exchange must
	// before it knows the account.
	APIKey(ctx context.Context, id kernel.ULID) (apikey.Key, error)
	// TouchAPIKey records a use of the key id, at most once a minute.
	TouchAPIKey(ctx context.Context, id kernel.ULID) error
}
