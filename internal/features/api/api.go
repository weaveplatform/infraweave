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

// Entitlement is an organization's entitlement: its tier, the version
// every change to it bumps, its features, sorted, and its limit values by
// limit name. A limit absent from Limits has no limit.
//
// ref: decision 0021
type Entitlement struct {
	Tier     string
	Version  int64
	Features []string
	Limits   map[string]int64
}

// Seats is the limit on members and API keys, which access counts.
//
// ref: decision 0021
const Seats = "seats"

// Usage is a counted thing's use at the moment of a creation: across the
// organization's accounts, and in the account the creation is for.
type Usage struct {
	Organization int64
	Account      int64
}

// Scope is where a limit was reached.
type Scope string

// The scopes a limit-reached problem names: the organization's pool, or
// one account's limit.
//
// ref: decision 0021
const (
	ScopeOrganization Scope = "organization"
	ScopeAccount      Scope = "account"
)

// LimitError refuses a creation that would take the organization above its
// pool or the account above its limit: the limit-reached problem, whose
// body members are Limit, Scope and Value.
//
// ref: decision 0021
type LimitError struct {
	Limit string
	Scope Scope
	// Value is the limit of Scope.
	Value int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("features: %s reached its %s limit of %d", e.Limit, e.Scope, e.Value)
}

// AccountLimitError refuses an account limit: Unknown when Limit is no
// limit of the catalogue, else Value is negative or above Pool, the
// organization's value of the limit.
//
// ref: decision 0021 § "Limits"
type AccountLimitError struct {
	Limit   string
	Value   int64
	Pool    int64
	Unknown bool
}

func (e *AccountLimitError) Error() string {
	switch {
	case e.Unknown:
		return fmt.Sprintf("features: %q is no limit", e.Limit)
	case e.Value < 0:
		return fmt.Sprintf("features: an account limit on %s cannot be negative", e.Limit)
	}
	return fmt.Sprintf("features: an account limit of %d on %s is above the organization's %d",
		e.Value, e.Limit, e.Pool)
}

// Gate is what a request needs of the catalogue: Feature, the capability
// its operation's x-infraweave-feature names, and Options, the gated
// values its body may set.
type Gate struct {
	Feature string
	Options []OptionGate
}

// OptionGate is one gated value: a request body whose value at Pointer, a
// JSON Pointer such as /oidc or /vendorConfig/terraform, is Value needs
// the option Feature; an empty Value is any value but null.
type OptionGate struct {
	Pointer string
	Value   string
	Feature string
}

// Entitlements is what other domains call of the features domain. Each
// write runs in the transaction ctx carries, so a domain creates or deletes
// an organization's or account's rows in the transaction that creates or
// deletes the organization or account.
type Entitlements interface {
	// CreateOrganization gives org its entitlement on the installation's
	// default tier, Enterprise unless the operator changed it.
	CreateOrganization(ctx context.Context, org kernel.OrganizationID) error
	// DeleteOrganization deletes org's entitlement, grants and account
	// limits; an organization without any is no error.
	DeleteOrganization(ctx context.Context, org kernel.OrganizationID) error
	// SetAccountLimit sets acct's limit on the counted thing limit, which
	// org's entitlement must exist to hold. A name that is no limit of the
	// catalogue, a negative value, or a value above the organization's
	// pool as it applies now is an *AccountLimitError.
	SetAccountLimit(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		limit string,
		value int64,
	) error
	// ClearAccountLimit removes acct's limit on limit, so the pool alone
	// applies; clearing none is no error. A name that is no limit of the
	// catalogue is an *AccountLimitError.
	ClearAccountLimit(ctx context.Context, acct kernel.AccountID, limit string) error
	// LimitNames are the catalogue's limits, in name order.
	LimitNames() []string
	// AccountLimits are acct's limits by limit name.
	AccountLimits(ctx context.Context, acct kernel.AccountID) (map[string]int64, error)
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
