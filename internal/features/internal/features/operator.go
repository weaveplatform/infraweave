package features

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Errors of the operator's writes. A tier or an organization the operator
// names that does not exist is featuresapi.ErrNotFound.
var (
	// ErrInvalid is input the catalogue or the naming rule refuses.
	ErrInvalid = errors.New("features: invalid")
	// ErrExists is a tier name another tier has.
	ErrExists = errors.New("features: the tier exists")
	// ErrInvalidState is a write the tier's state refuses: Enterprise, or
	// a tier an organization or the default holds.
	ErrInvalidState = errors.New("features: invalid state")
	// ErrUnknownTier is a tier a body names, which, unlike a tier the path
	// names, is invalid input rather than a missing resource.
	ErrUnknownTier = errors.New("features: no such tier")
)

// OperatorStore is what the operator's operations need of storage, part of
// Store. Every write takes effect on every replica: it
// notifies infraweave_invalidate in its transaction, and a write to one
// organization's tier or grants bumps its entitlement_version.
type OperatorStore interface {
	// HeldTiers reads every tier with how many organizations hold it, in
	// name order.
	HeldTiers(ctx context.Context) ([]Held, error)
	CreateTier(ctx context.Context, t features.Tier) error
	// ReplaceTier replaces an existing tier's content.
	ReplaceTier(ctx context.Context, t features.Tier) error
	// DeleteTier deletes a tier no organization and no default holds.
	DeleteTier(ctx context.Context, name string) error
	BillingState(ctx context.Context) (Billing, error)
	// SetBilling sets the switch and, when defaultTier is not nil, the
	// default tier.
	SetBilling(ctx context.Context, enabled bool, defaultTier *string) error
	Grants(ctx context.Context, org kernel.OrganizationID) (Grants, error)
	SetTier(ctx context.Context, org kernel.OrganizationID, tier string) error
	GrantFeature(ctx context.Context, org kernel.OrganizationID, feature string) error
	RevokeFeature(ctx context.Context, org kernel.OrganizationID, feature string) error
	// GrantLimit sets org's value of limit, nil lifting it.
	GrantLimit(ctx context.Context, org kernel.OrganizationID, limit string, value *int64) error
	RevokeLimit(ctx context.Context, org kernel.OrganizationID, limit string) error
}

// Held is a tier with how many organizations hold it.
type Held struct {
	features.Tier
	Organizations int64
}

// Editable reports whether the operator may change or delete the tier:
// every tier but Enterprise, which is what every organization has while
// billing is off.
func (h Held) Editable() bool { return h.Name != features.Enterprise }

// Billing is the installation's switch and the tier a new organization
// gets.
type Billing struct {
	Enabled     bool
	DefaultTier string
}

// Grants are what the operator grants an organization beyond its tier:
// features, sorted, and limit values by limit name, nil lifting the limit.
type Grants struct {
	Features []string
	Limits   map[string]*int64
}

// Operated is an organization's entitlement as the operator reads it: as
// it applies while billing is on, whatever the switch, with its grants.
type Operated struct {
	featuresapi.Entitlement
	Grants Grants
}

// tierName is decision 0021's tier name: the seeded names, Starter+ among
// them, and the operator's own.
var tierName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 +._-]{0,62}$`)

// CheckTier validates a tier's content against the catalogue: each feature
// a capability or option, each once, and each limit a limit of it.
func CheckTier(t features.Tier) error {
	var errs []error
	for i, name := range t.Features {
		f, ok := features.Lookup(name)
		switch {
		case !ok || !f.Gated():
			errs = append(errs, fmt.Errorf("%w: features[%d] %q is not a capability or option",
				ErrInvalid, i, name))
		case slices.Contains(t.Features[:i], name):
			errs = append(
				errs,
				fmt.Errorf("%w: features[%d] %q is named twice", ErrInvalid, i, name),
			)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(t.Limits)) {
		if err := checkLimit("limits."+name, name); err != nil {
			errs = append(errs, err)
		} else if t.Limits[name] < 0 {
			errs = append(errs, fmt.Errorf("%w: limits.%s is below 0", ErrInvalid, name))
		}
	}
	return errors.Join(errs...)
}

func checkLimit(field, name string) error {
	if f, ok := features.Lookup(name); !ok || f.Kind != features.Limit {
		return fmt.Errorf("%w: %s: %q is not a limit", ErrInvalid, field, name)
	}
	return nil
}

// Tiers is every tier, in name order.
func (s *Service) Tiers(ctx context.Context) ([]Held, error) {
	return s.store.HeldTiers(ctx) //nolint:wrapcheck // the store names itself
}

// Tier is the tier name.
func (s *Service) Tier(ctx context.Context, name string) (Held, error) {
	ts, err := s.Tiers(ctx)
	if err != nil {
		return Held{}, err
	}
	i := slices.IndexFunc(ts, func(h Held) bool { return h.Name == name })
	if i < 0 {
		return Held{}, fmt.Errorf("%w: tier %q", featuresapi.ErrNotFound, name)
	}
	return ts[i], nil
}

// CreateTier creates t.
func (s *Service) CreateTier(ctx context.Context, t features.Tier) (Held, error) {
	if !tierName.MatchString(t.Name) {
		return Held{}, fmt.Errorf("%w: name: 1 to 63 letters, digits, spaces and + . _ -, "+
			"starting with a letter or digit", ErrInvalid)
	}
	if err := CheckTier(t); err != nil {
		return Held{}, err
	}
	if err := s.store.CreateTier(ctx, sorted(t)); err != nil {
		return Held{}, err //nolint:wrapcheck // as above
	}
	return s.Tier(ctx, t.Name)
}

// ReplaceTier replaces the content of the tier t names.
func (s *Service) ReplaceTier(ctx context.Context, t features.Tier) (Held, error) {
	if err := s.editable(ctx, t.Name); err != nil {
		return Held{}, err
	}
	if err := CheckTier(t); err != nil {
		return Held{}, err
	}
	if err := s.store.ReplaceTier(ctx, sorted(t)); err != nil {
		return Held{}, err //nolint:wrapcheck // as above
	}
	return s.Tier(ctx, t.Name)
}

// DeleteTier deletes the tier name and answers it as it was.
func (s *Service) DeleteTier(ctx context.Context, name string) (Held, error) {
	if err := s.editable(ctx, name); err != nil {
		return Held{}, err
	}
	was, err := s.Tier(ctx, name)
	if err != nil {
		return Held{}, err
	}
	if err := s.store.DeleteTier(ctx, name); err != nil {
		return Held{}, err //nolint:wrapcheck // as above
	}
	return was, nil
}

// editable refuses Enterprise before the tier is looked up, so its 409
// does not depend on whether it was seeded yet.
func (s *Service) editable(ctx context.Context, name string) error {
	if !(Held{Tier: features.Tier{Name: name}}).Editable() {
		return fmt.Errorf("%w: %s holds every feature and no limit, and cannot be changed",
			ErrInvalidState, name)
	}
	_, err := s.Tier(ctx, name)
	return err
}

func sorted(t features.Tier) features.Tier {
	t.Features = slices.Sorted(slices.Values(t.Features))
	if t.Limits == nil {
		t.Limits = map[string]int64{}
	}
	return t
}

// BillingState reads the switch and the default tier from storage, not
// the snapshot, so the operator reads what it last set.
func (s *Service) BillingState(ctx context.Context) (Billing, error) {
	return s.store.BillingState(ctx) //nolint:wrapcheck // as above
}

// SetBilling sets the switch and, when defaultTier is not nil, the
// default tier.
func (s *Service) SetBilling(
	ctx context.Context,
	enabled bool,
	defaultTier *string,
) (Billing, error) {
	if err := s.store.SetBilling(ctx, enabled, defaultTier); err != nil {
		return Billing{}, unknownTier(err) //nolint:wrapcheck // as above
	}
	return s.BillingState(ctx)
}

// unknownTier turns a tier a body names that does not exist into
// ErrUnknownTier.
func unknownTier(err error) error {
	if errors.Is(err, featuresapi.ErrNotFound) {
		return fmt.Errorf("%w: %w", ErrUnknownTier, err)
	}
	return err
}

// OrganizationEntitlement is org's entitlement as it applies while billing
// is on, with its grants.
func (s *Service) OrganizationEntitlement(
	ctx context.Context,
	org kernel.OrganizationID,
) (Operated, error) {
	e, err := s.store.Entitlement(ctx, org, false)
	if err != nil {
		return Operated{}, err //nolint:wrapcheck // as above
	}
	e.Features = features.Closed(slices.DeleteFunc(e.Features, s.switchedOff))
	g, err := s.store.Grants(ctx, org)
	if err != nil {
		return Operated{}, err //nolint:wrapcheck // as above
	}
	return Operated{Entitlement: e, Grants: g}, nil
}

// SetTier moves org to tier.
func (s *Service) SetTier(
	ctx context.Context,
	org kernel.OrganizationID,
	tier string,
) (Operated, error) {
	if err := s.store.SetTier(ctx, org, tier); err != nil {
		return Operated{}, err //nolint:wrapcheck // as above
	}
	return s.OrganizationEntitlement(ctx, org)
}

// GrantFeature grants org feature, a capability or option.
func (s *Service) GrantFeature(
	ctx context.Context,
	org kernel.OrganizationID,
	feature string,
) (Operated, error) {
	if f, ok := features.Lookup(feature); !ok || !f.Gated() {
		return Operated{}, fmt.Errorf("%w: %q is not a capability or option", ErrInvalid, feature)
	}
	if err := s.store.GrantFeature(ctx, org, feature); err != nil {
		return Operated{}, err //nolint:wrapcheck // as above
	}
	return s.OrganizationEntitlement(ctx, org)
}

// RevokeFeature removes org's grant of feature.
func (s *Service) RevokeFeature(
	ctx context.Context,
	org kernel.OrganizationID,
	feature string,
) (Operated, error) {
	if err := s.store.RevokeFeature(ctx, org, feature); err != nil {
		return Operated{}, err //nolint:wrapcheck // as above
	}
	return s.OrganizationEntitlement(ctx, org)
}

// GrantLimit sets org's value of limit, nil lifting it.
func (s *Service) GrantLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	limit string,
	value *int64,
) (Operated, error) {
	if err := checkLimit("limit", limit); err != nil {
		return Operated{}, err
	}
	if value != nil && *value < 0 {
		return Operated{}, fmt.Errorf("%w: value is below 0", ErrInvalid)
	}
	if err := s.store.GrantLimit(ctx, org, limit, value); err != nil {
		return Operated{}, err //nolint:wrapcheck // as above
	}
	return s.OrganizationEntitlement(ctx, org)
}

// RevokeLimit removes org's grant of limit, so the tier's value applies.
func (s *Service) RevokeLimit(
	ctx context.Context,
	org kernel.OrganizationID,
	limit string,
) (Operated, error) {
	if err := s.store.RevokeLimit(ctx, org, limit); err != nil {
		return Operated{}, err //nolint:wrapcheck // as above
	}
	return s.OrganizationEntitlement(ctx, org)
}
