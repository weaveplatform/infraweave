package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// OrganizationTier is the tier org is on, as the operator defines it:
// Enterprise while billing is off, which every organization then has, and
// for an organization the operator has not given an entitlement, which
// Allow refuses nothing.
func (s *Service) OrganizationTier(
	ctx context.Context,
	org kernel.OrganizationID,
) (features.Tier, error) {
	name := features.Enterprise
	on, err := s.Billing(ctx)
	if err != nil {
		return features.Tier{}, err
	}
	if on {
		e, err := s.Entitlement(ctx, org)
		switch {
		case err == nil:
			name = e.Tier
		case !errors.Is(err, featuresapi.ErrNotFound):
			return features.Tier{}, err
		}
	}
	ts, err := s.contents(ctx)
	if err != nil {
		return features.Tier{}, err
	}
	// A tier an organization holds cannot be deleted, so only a store
	// read between the two reads above can miss it.
	i := slices.IndexFunc(ts, func(t features.Tier) bool { return t.Name == name })
	if i < 0 {
		return features.Tier{}, fmt.Errorf("%w: tier %q", featuresapi.ErrNotFound, name)
	}
	return sorted(ts[i]), nil
}

// Entitled is the features org's entitlement holds, sorted; an
// organization the operator has not given an entitlement holds what
// Enterprise does, as Allow refuses it nothing the operator left on.
func (s *Service) Entitled(ctx context.Context, org kernel.OrganizationID) ([]string, error) {
	e, err := s.Entitlement(ctx, org)
	if errors.Is(err, featuresapi.ErrNotFound) {
		return features.Closed(slices.DeleteFunc(features.Gated(), s.switchedOff)), nil
	}
	return e.Features, err
}

// SeatLimit is the seats in use under a seats limit, and the limit, nil
// for none.
type SeatLimit struct {
	InUse int64
	Limit *int64
}

// Seats are an account's seats: its organization's pool and, when the
// account has a seats limit of its own, the account's.
type Seats struct {
	Organization SeatLimit
	Account      *SeatLimit
}

// Seats are acct's seats in org with used the seats in use, which the
// access domain counts. With billing off no limit applies, as CheckLimit
// admits every seat, so neither is set.
func (s *Service) Seats(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
	used featuresapi.Usage,
) (Seats, error) {
	out := Seats{Organization: SeatLimit{InUse: used.Organization}}
	on, err := s.Billing(ctx)
	if err != nil || !on {
		return out, err
	}
	e, err := s.Entitlement(ctx, org)
	switch {
	case errors.Is(err, featuresapi.ErrNotFound):
		return out, nil
	case err != nil:
		return out, err
	}
	if v, ok := e.Limits[featuresapi.Seats]; ok {
		out.Organization.Limit = &v
	}
	v, ok, err := s.store.AccountLimit(ctx, acct, featuresapi.Seats)
	if err != nil {
		return out, err //nolint:wrapcheck // the store names itself
	}
	if ok {
		out.Account = &SeatLimit{Limit: &v, InUse: used.Account}
	}
	return out, nil
}
