package features

import (
	"context"
	"errors"
	"fmt"
	"slices"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Store is what the service needs of storage; pg.Store is one. Every
// method joins the transaction ctx carries.
type Store interface {
	CreateOrganization(ctx context.Context, org kernel.OrganizationID, tier string) error
	DeleteOrganization(ctx context.Context, org kernel.OrganizationID) error
	SetAccountLimit(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		limit string,
		value int64,
	) error
	DeleteAccount(ctx context.Context, acct kernel.AccountID) error
	// Entitlement reads org's entitlement, locking its row until ctx's
	// transaction ends when lock is set.
	Entitlement(
		ctx context.Context,
		org kernel.OrganizationID,
		lock bool,
	) (featuresapi.Entitlement, error)
	AccountLimit(ctx context.Context, acct kernel.AccountID, limit string) (int64, bool, error)
}

// Transactor runs fn in a transaction that ctx carries to the store,
// joining one ctx already carries.
type Transactor func(ctx context.Context, fn func(ctx context.Context) error) error

// Service is featuresapi.Entitlements over a Store.
type Service struct {
	store    Store
	inTx     Transactor
	disabled []string
}

var _ featuresapi.Entitlements = (*Service)(nil)

// New returns the service on store, running a limit check in a
// transaction through inTx, with disabled the installation's
// features.disabled, which no entitlement holds.
func New(store Store, inTx Transactor, disabled []string) *Service {
	return &Service{store: store, inTx: inTx, disabled: disabled}
}

// CreateOrganization gives org its entitlement on tier.
func (s *Service) CreateOrganization(
	ctx context.Context,
	org kernel.OrganizationID,
	tier string,
) error {
	return s.store.CreateOrganization(ctx, org, tier) //nolint:wrapcheck // the store names itself
}

// DeleteOrganization deletes org's entitlement, grants and account limits.
func (s *Service) DeleteOrganization(ctx context.Context, org kernel.OrganizationID) error {
	return s.store.DeleteOrganization(ctx, org) //nolint:wrapcheck // as above
}

// SetAccountLimit sets acct's limit on limit.
func (s *Service) SetAccountLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	limit string,
	value int64,
) error {
	return s.store.SetAccountLimit(ctx, org, acct, limit, value) //nolint:wrapcheck // as above
}

// DeleteAccount deletes acct's limits.
func (s *Service) DeleteAccount(ctx context.Context, acct kernel.AccountID) error {
	return s.store.DeleteAccount(ctx, acct) //nolint:wrapcheck // as above
}

// Entitlement reads org's entitlement, less the switched-off features.
func (s *Service) Entitlement(
	ctx context.Context,
	org kernel.OrganizationID,
) (featuresapi.Entitlement, error) {
	e, err := s.store.Entitlement(ctx, org, false)
	if err != nil {
		return e, err //nolint:wrapcheck // as above
	}
	e.Features = slices.DeleteFunc(e.Features, func(f string) bool {
		return slices.Contains(s.disabled, f)
	})
	return e, nil
}

// CheckLimit admits one more limit in acct, or refuses it with a
// *featuresapi.LimitError. The pool is checked first, so a creation that
// passes both names the organization, which no account admin can raise.
func (s *Service) CheckLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	limit string,
	count func(context.Context) (featuresapi.Usage, error),
) error {
	return s.inTx(ctx, func(ctx context.Context) error {
		e, err := s.store.Entitlement(ctx, org, true)
		if errors.Is(err, featuresapi.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err //nolint:wrapcheck // as above
		}
		pool, pooled := e.Limits[limit]
		ceiling, capped, err := s.store.AccountLimit(ctx, acct, limit)
		if err != nil || !pooled && !capped {
			return err //nolint:wrapcheck // as above
		}
		used, err := count(ctx)
		if err != nil {
			return fmt.Errorf("count %s: %w", limit, err)
		}
		if pooled && used.Organization >= pool {
			return &featuresapi.LimitError{
				Limit: limit, Scope: featuresapi.ScopeOrganization, Value: pool,
			}
		}
		if capped && used.Account >= ceiling {
			return &featuresapi.LimitError{
				Limit: limit, Scope: featuresapi.ScopeAccount, Value: ceiling,
			}
		}
		return nil
	})
}
