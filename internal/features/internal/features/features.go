package features

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Store is what the service needs of storage; pg.Store is one. Every
// method joins the transaction ctx carries.
type Store interface {
	OperatorStore
	// CreateOrganization gives org its entitlement on the default tier.
	CreateOrganization(ctx context.Context, org kernel.OrganizationID) error
	DeleteOrganization(ctx context.Context, org kernel.OrganizationID) error
	SetAccountLimit(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		limit string,
		value int64,
	) error
	DeleteAccount(ctx context.Context, acct kernel.AccountID) error
	// Entitlement reads org's tier, grants and limits, locking its row
	// until ctx's transaction ends when lock is set.
	Entitlement(
		ctx context.Context,
		org kernel.OrganizationID,
		lock bool,
	) (featuresapi.Entitlement, error)
	AccountLimit(ctx context.Context, acct kernel.AccountID, limit string) (int64, bool, error)
	Billing(ctx context.Context) (bool, error)
	// Tiers reads every tier's content.
	Tiers(ctx context.Context) ([]features.Tier, error)
	// Seed installs the seeded tiers, or brings forward those the operator
	// has not changed.
	Seed(ctx context.Context, seeds []features.Tier) error
}

// Transactor runs fn in a transaction that ctx carries to the store,
// joining one ctx already carries.
type Transactor func(ctx context.Context, fn func(ctx context.Context) error) error

// invalidatePrefix starts every payload on infraweave_invalidate that
// concerns this domain: InvalidateKey(org) for one organization's tier or
// grants, anything else for the billing switch or a tier, which reaches
// every organization.
const invalidatePrefix = "features:"

// InvalidateKey is the payload that makes every replica drop org's
// entitlement snapshot.
func InvalidateKey(org kernel.OrganizationID) string { return invalidatePrefix + org.String() }

// TiersKey is the payload that makes every replica drop every snapshot
// after a tier's content changes, which reaches every organization on it.
const TiersKey = invalidatePrefix + "tiers"

// BillingKey is the payload that makes every replica drop every snapshot
// after the billing switch changes, which turns every check on or off.
const BillingKey = invalidatePrefix + "billing"

// Service is featuresapi.Entitlements over a Store, with the replica's
// snapshot of each organization's entitlement, the billing switch and the
// tiers, which Invalidate drops.
type Service struct {
	store    Store
	inTx     Transactor
	disabled []string

	mu      sync.Mutex
	orgs    map[kernel.OrganizationID]featuresapi.Entitlement
	billing *bool
	tiers   []features.Tier
	// gen counts invalidations, so a read that began before one does not
	// store what it read after it.
	gen uint64
}

var _ featuresapi.Entitlements = (*Service)(nil)

// New returns the service on store, running a limit check in a
// transaction through inTx, with disabled the installation's
// features.disabled, which no entitlement holds.
func New(store Store, inTx Transactor, disabled []string) *Service {
	return &Service{
		store: store, inTx: inTx, disabled: disabled,
		orgs: map[kernel.OrganizationID]featuresapi.Entitlement{},
	}
}

// Seed installs the seeded tiers from the catalogue.
func (s *Service) Seed(ctx context.Context) error {
	seeds := make([]features.Tier, 0, len(features.SeededTiers))
	for _, name := range features.SeededTiers {
		t, _ := features.Seed(name) // a seeded tier's name, so always found
		seeds = append(seeds, t)
	}
	return s.store.Seed(ctx, seeds) //nolint:wrapcheck // the store names itself
}

// Invalidate drops what the payload of an infraweave_invalidate message
// concerns: one organization's snapshot, or every snapshot for another
// payload of this domain. It ignores other domains' payloads.
func (s *Service) Invalidate(payload string) {
	rest, ok := strings.CutPrefix(payload, invalidatePrefix)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	if id, err := kernel.ParseULID(rest); err == nil {
		delete(s.orgs, kernel.OrganizationID{ULID: id})
		return
	}
	s.flush()
}

// Flush drops every snapshot, as after a missed notification.
func (s *Service) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flush()
}

func (s *Service) flush() {
	s.gen++
	clear(s.orgs)
	s.billing, s.tiers = nil, nil
}

// CreateOrganization gives org its entitlement on the default tier.
func (s *Service) CreateOrganization(ctx context.Context, org kernel.OrganizationID) error {
	return s.store.CreateOrganization(ctx, org) //nolint:wrapcheck // the store names itself
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

// Entitlement is org's entitlement: its tier's features and its grants,
// less the switched-off features, less every feature whose requirements
// that leaves out. With billing off it reads as Enterprise, every gated
// feature and no limit, under the organization's own tier and version.
func (s *Service) Entitlement(
	ctx context.Context,
	org kernel.OrganizationID,
) (featuresapi.Entitlement, error) {
	s.mu.Lock()
	e, ok := s.orgs[org]
	gen := s.gen
	s.mu.Unlock()
	if ok {
		return e, nil
	}
	on, err := s.Billing(ctx)
	if err != nil {
		return e, err
	}
	e, err = s.store.Entitlement(ctx, org, false)
	if err != nil {
		return e, err //nolint:wrapcheck // as above
	}
	if !on {
		e.Features, e.Limits = features.Gated(), map[string]int64{}
	}
	e.Features = features.Closed(slices.DeleteFunc(e.Features, s.switchedOff))
	s.keep(gen, func() { s.orgs[org] = e })
	return e, nil
}

// keep runs set, which stores a read, unless an invalidation came after
// gen, when the read began.
func (s *Service) keep(gen uint64, set func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen == gen {
		set()
	}
}

func (s *Service) switchedOff(name string) bool { return slices.Contains(s.disabled, name) }

// Billing reads the billing switch.
func (s *Service) Billing(ctx context.Context) (bool, error) {
	s.mu.Lock()
	b, gen := s.billing, s.gen
	s.mu.Unlock()
	if b != nil {
		return *b, nil
	}
	on, err := s.store.Billing(ctx)
	if err != nil {
		return false, err //nolint:wrapcheck // as above
	}
	s.keep(gen, func() { s.billing = &on })
	return on, nil
}

// Refusal is why a request for a feature is refused: Disabled for the
// operator's switch-off, which no caller can change, else the caller's
// organization lacks it, and Tiers are the tiers that hold it.
type Refusal struct {
	Disabled bool
	Feature  string
	Tiers    []string
}

func (r *Refusal) Error() string {
	if r.Disabled {
		return "features: " + r.Feature + " is switched off"
	}
	return "features: " + r.Feature + " is not in the organization's tier"
}

// Allow refuses feature to the request ctx carries with a *Refusal when the
// installation switched it off, or, with billing on, when the principal's
// organization lacks it. A principal of no organization, an operator's or
// none, is refused only a switched-off feature; so is an organization the
// operator has not given an entitlement.
func (s *Service) Allow(ctx context.Context, feature string) error {
	p, _ := kernel.PrincipalFrom(ctx) // none is the zero principal, of no organization
	return s.AllowOrganization(ctx, p.OrganizationID, feature)
}

// AllowOrganization is Allow for org's work outside a request, such as a
// job; a zero org is refused only a switched-off feature.
func (s *Service) AllowOrganization(
	ctx context.Context,
	org kernel.OrganizationID,
	feature string,
) error {
	if s.switchedOff(feature) {
		return &Refusal{Disabled: true, Feature: feature}
	}
	if org.IsZero() {
		return nil
	}
	on, err := s.Billing(ctx)
	if err != nil || !on {
		return err
	}
	e, err := s.Entitlement(ctx, org)
	switch {
	case errors.Is(err, featuresapi.ErrNotFound):
		return nil
	case err != nil:
		return err
	case slices.Contains(e.Features, feature):
		return nil
	}
	holding, err := s.holding(ctx, feature)
	if err != nil {
		return err
	}
	return &Refusal{Feature: feature, Tiers: holding}
}

// holding names the tiers that hold feature, smallest first: fewest
// features, then name.
func (s *Service) holding(ctx context.Context, feature string) ([]string, error) {
	s.mu.Lock()
	ts, gen := s.tiers, s.gen
	s.mu.Unlock()
	if ts == nil {
		var err error
		if ts, err = s.store.Tiers(ctx); err != nil {
			return nil, err //nolint:wrapcheck // as above
		}
		s.keep(gen, func() { s.tiers = ts })
	}
	ts = slices.Clone(ts)
	slices.SortFunc(ts, func(a, b features.Tier) int {
		return cmp.Or(
			cmp.Compare(len(a.Features), len(b.Features)),
			strings.Compare(a.Name, b.Name),
		)
	})
	out := []string{}
	for _, t := range ts {
		if slices.Contains(t.Features, feature) {
			out = append(out, t.Name)
		}
	}
	return out, nil
}

// CheckLimit admits one more limit in acct, or refuses it with a
// *featuresapi.LimitError; with billing off it admits everything. The pool
// is checked first, so a creation that passes both names the
// organization, which no account admin can raise.
func (s *Service) CheckLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	limit string,
	count func(context.Context) (featuresapi.Usage, error),
) error {
	return s.inTx(ctx, func(ctx context.Context) error {
		// Read in the creating transaction rather than from the snapshot,
		// so a switch turned on a moment ago already counts.
		on, err := s.store.Billing(ctx)
		if err != nil || !on {
			return err //nolint:wrapcheck // as above
		}
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
