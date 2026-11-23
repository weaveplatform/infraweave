package core

import (
	"context"
	"errors"
	"slices"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PreviewStore is what the service needs of storage for feature previews;
// pg.Store is one. Every method joins the transaction ctx carries.
type PreviewStore interface {
	// FeaturePreviews reads acct's opt-in, false for an account that has
	// never set it.
	FeaturePreviews(ctx context.Context, acct kernel.AccountID) (bool, error)
	// SetFeaturePreviews sets acct's opt-in and notifies AccountKey(acct)
	// on infraweave_invalidate when the write commits.
	SetFeaturePreviews(ctx context.Context, acct kernel.AccountID, enabled bool) error
	// PersonalFeaturePreviews reads member's setting in acct, true for a
	// member who has never opted out.
	PersonalFeaturePreviews(ctx context.Context, acct kernel.AccountID, member kernel.MemberID) (bool, error)
	SetPersonalFeaturePreviews(ctx context.Context, acct kernel.AccountID, member kernel.MemberID, enabled bool) error
	RemoveMember(ctx context.Context, acct kernel.AccountID, member kernel.MemberID) error
}

var _ featuresapi.Previews = (*Service)(nil)

// preview reports whether feature is in preview.
func (s *Service) preview(feature string) bool {
	return slices.ContainsFunc(s.previews(), func(f features.Feature) bool { return f.Name == feature })
}

// optedIn reports whether acct has opted in to feature previews; a zero
// acct has no setting and has not.
func (s *Service) optedIn(ctx context.Context, acct kernel.AccountID) (bool, error) {
	if acct.IsZero() {
		return false, nil
	}
	return s.FeaturePreviewsEnabled(ctx, acct)
}

// FeaturePreviewsEnabled reads acct's opt-in to feature previews through
// the replica's snapshot, which Invalidate drops on AccountKey(acct).
func (s *Service) FeaturePreviewsEnabled(ctx context.Context, acct kernel.AccountID) (bool, error) {
	s.mu.Lock()
	on, ok := s.accounts[acct]
	gen := s.gen
	s.mu.Unlock()
	if ok {
		return on, nil
	}
	on, err := s.store.FeaturePreviews(ctx, acct)
	if err != nil {
		return false, err //nolint:wrapcheck // the store names itself
	}
	s.keep(gen, func() { s.accounts[acct] = on })
	return on, nil
}

// SetFeaturePreviewsEnabled sets acct's opt-in; every replica, this one
// included, applies it once the write commits and its notification
// arrives.
func (s *Service) SetFeaturePreviewsEnabled(ctx context.Context, acct kernel.AccountID, enabled bool) error {
	return s.store.SetFeaturePreviews(ctx, acct, enabled) //nolint:wrapcheck // as above
}

// PersonalFeaturePreviewsEnabled reads member's setting in acct. No
// feature lookup reads it, so the replica keeps no snapshot of it.
func (s *Service) PersonalFeaturePreviewsEnabled(ctx context.Context, acct kernel.AccountID, member kernel.MemberID) (bool, error) {
	return s.store.PersonalFeaturePreviews(ctx, acct, member) //nolint:wrapcheck // as above
}

// SetPersonalFeaturePreviewsEnabled sets member's setting in acct.
func (s *Service) SetPersonalFeaturePreviewsEnabled(ctx context.Context, acct kernel.AccountID, member kernel.MemberID, enabled bool) error {
	return s.store.SetPersonalFeaturePreviews(ctx, acct, member, enabled) //nolint:wrapcheck // as above
}

// RemoveMember deletes member's setting in acct.
func (s *Service) RemoveMember(ctx context.Context, acct kernel.AccountID, member kernel.MemberID) error {
	return s.store.RemoveMember(ctx, acct, member) //nolint:wrapcheck // as above
}

// Active reports whether a preview is active for a caller: held, the
// account's entitlement holds it; optedIn, the account has opted in to
// feature previews; and personal, the member's own setting, nil for a
// principal that names no member.
//
// ref: decision 0021
func Active(held, optedIn bool, personal *bool) bool {
	return held && optedIn && (personal == nil || *personal)
}

// PreviewStatus is one feature in preview and whether it is active for
// the caller.
type PreviewStatus struct {
	features.Feature
	Active bool
}

// Previews are the feature previews as one principal sees them.
type Previews struct {
	// Features are every preview features.disabled leaves, in catalogue
	// order.
	Features []PreviewStatus
	// Account is the account's opt-in.
	Account bool
	// Personal is the member's own setting; nil for a principal that names
	// no member.
	Personal *bool
}

// FeaturePreviews reads the feature previews for p: each preview the
// installation has not switched off, active when p's organization holds
// it, p's account has opted in and, for a principal that names a member,
// the member has not opted out; the account's opt-in; and the member's
// own setting.
func (s *Service) FeaturePreviews(ctx context.Context, p kernel.Principal) (Previews, error) {
	var out Previews
	var err error
	if out.Account, err = s.optedIn(ctx, p.AccountID); err != nil {
		return Previews{}, err
	}
	if !p.MemberID.IsZero() {
		personal, err := s.PersonalFeaturePreviewsEnabled(ctx, p.AccountID, p.MemberID)
		if err != nil {
			return Previews{}, err
		}
		out.Personal = &personal
	}
	out.Features = []PreviewStatus{}
	for _, f := range s.previews() {
		if s.switchedOff(f.Name) {
			continue
		}
		held := true
		if !p.OrganizationID.IsZero() {
			err := s.inTier(ctx, p.OrganizationID, f.Name)
			if _, refused := errors.AsType[*Refusal](err); refused {
				held = false
			} else if err != nil {
				return Previews{}, err
			}
		}
		out.Features = append(out.Features, PreviewStatus{
			Feature: f, Active: Active(held, out.Account, out.Personal),
		})
	}
	return out, nil
}
