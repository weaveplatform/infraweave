package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrNotFound is an organization without an entitlement, or a tier that
// does not exist.
var ErrNotFound = errors.New("features: not found")

// Entitlement is an organization's entitlement (decision 0021): its tier,
// the version every change to it bumps, its features, sorted, and its limit
// values by limit name. A limit absent from Limits has no limit.
type Entitlement struct {
	Tier     string
	Version  int64
	Features []string
	Limits   map[string]int64
}

// Usage is a counted thing's use at the moment of a creation: across the
// organization's accounts, and in the account the creation is for.
type Usage struct {
	Organization int64
	Account      int64
}

// Scope is where a limit was reached.
type Scope string

// The scopes of decision 0021's limit-reached.
const (
	ScopeOrganization Scope = "organization"
	ScopeAccount      Scope = "account"
)

// LimitError refuses a creation that would take the organization above its
// pool or the account above its limit: decision 0021's limit-reached, whose
// body members are Limit, Scope and Value.
type LimitError struct {
	Limit string
	Scope Scope
	// Value is the limit of Scope.
	Value int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("features: %s reached its %s limit of %d", e.Limit, e.Scope, e.Value)
}

// Entitlements is what other domains call of the features domain. Each
// write runs in the transaction ctx carries, so a domain creates or deletes
// an organization's or account's rows in the transaction that creates or
// deletes the organization or account.
type Entitlements interface {
	// CreateOrganization gives org its entitlement on tier, which must
	// exist.
	CreateOrganization(ctx context.Context, org kernel.OrganizationID, tier string) error
	// DeleteOrganization deletes org's entitlement, grants and account
	// limits; an organization without any is no error.
	DeleteOrganization(ctx context.Context, org kernel.OrganizationID) error
	// SetAccountLimit sets acct's limit on the counted thing limit, which
	// org's entitlement must exist to hold.
	SetAccountLimit(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		limit string,
		value int64,
	) error
	// DeleteAccount deletes acct's limits.
	DeleteAccount(ctx context.Context, acct kernel.AccountID) error
	// Entitlement reads org's entitlement.
	Entitlement(ctx context.Context, org kernel.OrganizationID) (Entitlement, error)
	// CheckLimit admits the creation of one more limit in acct: it locks
	// org's entitlement, counts with count while holding the lock, and
	// answers a *LimitError when one more would pass the organization's
	// pool or, failing that, acct's limit. Called in the creating
	// transaction, the lock holds until the creation commits, so two
	// creations cannot both take the last unit. An organization without
	// an entitlement has no limits.
	CheckLimit(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		limit string,
		count func(context.Context) (Usage, error),
	) error
}
