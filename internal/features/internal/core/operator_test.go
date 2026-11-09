package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	root "github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The operator half of store: held are the tiers HeldTiers answers, and
// each write records itself in calls and fails with opErr.
func (s *store) HeldTiers(context.Context) ([]core.Held, error) {
	return slices.Clone(s.held), s.tiersErr
}

func (s *store) CreateTier(_ context.Context, t root.Tier) error {
	s.calls = append(s.calls, "create tier "+t.Name)
	if s.opErr == nil {
		s.held = append(s.held, core.Held{Tier: t})
	}
	return s.opErr
}

func (s *store) ReplaceTier(_ context.Context, t root.Tier) error {
	s.calls = append(s.calls, "replace tier "+t.Name)
	for i := range s.held {
		if s.held[i].Name == t.Name && s.opErr == nil {
			s.held[i].Tier = t
		}
	}
	return s.opErr
}

func (s *store) DeleteTier(_ context.Context, name string) error {
	s.calls = append(s.calls, "delete tier "+name)
	return s.opErr
}

func (s *store) BillingState(context.Context) (core.Billing, error) {
	return s.billing, s.billingErr
}

func (s *store) SetBilling(_ context.Context, enabled bool, tier *string) error {
	s.calls = append(s.calls, "billing")
	if s.opErr == nil {
		s.billing.Enabled = enabled
		if tier != nil {
			s.billing.DefaultTier = *tier
		}
	}
	return s.opErr
}

func (s *store) Grants(context.Context, kernel.OrganizationID) (core.Grants, error) {
	return s.grants, s.grantsErr
}

func (s *store) SetTier(_ context.Context, _ kernel.OrganizationID, tier string) error {
	s.calls = append(s.calls, "tier "+tier)
	return s.opErr
}

func (s *store) GrantFeature(_ context.Context, _ kernel.OrganizationID, f string) error {
	s.calls = append(s.calls, "grant "+f)
	return s.opErr
}

func (s *store) RevokeFeature(_ context.Context, _ kernel.OrganizationID, f string) error {
	s.calls = append(s.calls, "revoke "+f)
	return s.opErr
}

func (s *store) GrantLimit(_ context.Context, _ kernel.OrganizationID, l string, _ *int64) error {
	s.calls = append(s.calls, "grant "+l)
	return s.opErr
}

func (s *store) RevokeLimit(_ context.Context, _ kernel.OrganizationID, l string) error {
	s.calls = append(s.calls, "revoke "+l)
	return s.opErr
}

func TestUnitCheckTier(t *testing.T) {
	ok := root.Tier{Features: []string{"stacks", "scans"}, Limits: map[string]int64{"seats": 3}}
	if err := core.CheckTier(ok); err != nil {
		t.Errorf("a valid tier: %v", err)
	}
	for name, c := range map[string]struct {
		tier root.Tier
		says string
	}{
		"unknown":  {root.Tier{Features: []string{"STACKS"}}, `features[0] "STACKS"`},
		"limit":    {root.Tier{Features: []string{"seats"}}, `features[0] "seats"`},
		"switch":   {root.Tier{Features: []string{"billing"}}, `features[0] "billing"`},
		"twice":    {root.Tier{Features: []string{"stacks", "stacks"}}, "named twice"},
		"no limit": {root.Tier{Limits: map[string]int64{"stacks": 1}}, `limits.stacks: "stacks"`},
		"negative": {root.Tier{Limits: map[string]int64{"seats": -1}}, "below 0"},
	} {
		err := core.CheckTier(c.tier)
		if !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want ErrInvalid naming %s", name, err, c.says)
		}
	}
}

func TestUnitOperatorTiers(t *testing.T) {
	ctx := t.Context()
	s := &store{held: []core.Held{
		{Tier: root.Tier{Name: root.Enterprise}, Organizations: 2},
		{Tier: root.Tier{Name: "Starter"}},
	}}
	svc := core.New(s, inTx(new(int)), nil)

	if ts, err := svc.Tiers(ctx); err != nil || len(ts) != 2 || ts[0].Editable() ||
		!ts[1].Editable() {
		t.Errorf("Tiers = %+v, %v", ts, err)
	}
	if _, err := svc.Tier(ctx, "Gold"); !errors.Is(err, featuresapi.ErrNotFound) {
		t.Errorf("an unknown tier = %v", err)
	}
	gold := root.Tier{Name: "Gold +1", Features: []string{"stacks", "scans"}}
	if h, err := svc.CreateTier(ctx, gold); err != nil || h.Name != gold.Name ||
		!slices.Equal(h.Features, []string{"scans", "stacks"}) || h.Limits == nil {
		t.Errorf("CreateTier = %+v, %v", h, err)
	}
	for _, bad := range []string{"", " lead", "a/b", strings.Repeat("a", 64)} {
		if _, err := svc.CreateTier(
			ctx,
			root.Tier{Name: bad},
		); !errors.Is(
			err,
			core.ErrInvalid,
		) {
			t.Errorf("name %q = %v", bad, err)
		}
	}
	if _, err := svc.CreateTier(
		ctx,
		root.Tier{Name: "Bad", Features: []string{"nope"}},
	); !errors.Is(
		err,
		core.ErrInvalid,
	) {
		t.Errorf("a bad feature = %v", err)
	}

	for _, name := range []string{root.Enterprise} {
		if _, err := svc.ReplaceTier(ctx, root.Tier{Name: name}); !errors.Is(err,
			core.ErrInvalidState) {
			t.Errorf("replacing %s = %v", name, err)
		}
		if _, err := svc.DeleteTier(ctx, name); !errors.Is(err, core.ErrInvalidState) {
			t.Errorf("deleting %s = %v", name, err)
		}
	}
	if _, err := svc.ReplaceTier(ctx, root.Tier{Name: "Gold"}); !errors.Is(err,
		featuresapi.ErrNotFound) {
		t.Errorf("replacing an unknown tier = %v", err)
	}
	if _, err := svc.ReplaceTier(
		ctx,
		root.Tier{Name: "Starter", Features: []string{"x"}},
	); !errors.Is(
		err,
		core.ErrInvalid,
	) {
		t.Errorf("replacing with a bad feature = %v", err)
	}
	if h, err := svc.ReplaceTier(
		ctx,
		root.Tier{Name: "Starter", Features: []string{"stacks"}},
	); err != nil ||
		!slices.Equal(h.Features, []string{"stacks"}) {
		t.Errorf("ReplaceTier = %+v, %v", h, err)
	}
	if h, err := svc.DeleteTier(ctx, "Starter"); err != nil || h.Name != "Starter" ||
		!slices.Contains(s.calls, "delete tier Starter") {
		t.Errorf("DeleteTier = %+v, %v, calls %v", h, err, s.calls)
	}

	s.opErr = errStore
	if _, err := svc.CreateTier(ctx, root.Tier{Name: "Silver"}); !errors.Is(err, errStore) {
		t.Errorf("CreateTier with the store down = %v", err)
	}
	for name, err := range map[string]error{
		"replace": second(svc.ReplaceTier(ctx, root.Tier{Name: "Starter"})),
		"delete":  second(svc.DeleteTier(ctx, "Starter")),
	} {
		if !errors.Is(err, errStore) {
			t.Errorf("%s with the store down = %v", name, err)
		}
	}
	s.opErr, s.tiersErr = nil, errStore
	if _, err := svc.DeleteTier(ctx, "Starter"); !errors.Is(err, errStore) {
		t.Errorf("DeleteTier with the tiers unreadable = %v", err)
	}
	if _, err := svc.CreateTier(ctx, root.Tier{Name: "Silver"}); !errors.Is(err, errStore) {
		t.Errorf("CreateTier answering unreadable tiers = %v", err)
	}
}

func second[T any](_ T, err error) error { return err }

func TestUnitOperatorBilling(t *testing.T) {
	ctx := t.Context()
	s := &store{billing: core.Billing{DefaultTier: root.Enterprise}}
	svc := core.New(s, inTx(new(int)), nil)
	free := root.Free
	if b, err := svc.SetBilling(
		ctx,
		true,
		&free,
	); err != nil || !b.Enabled ||
		b.DefaultTier != free {
		t.Errorf("SetBilling = %+v, %v", b, err)
	}
	if b, err := svc.SetBilling(ctx, false, nil); err != nil || b.Enabled || b.DefaultTier != free {
		t.Errorf("SetBilling keeping the default = %+v, %v", b, err)
	}
	s.opErr = featuresapi.ErrNotFound
	if _, err := svc.SetBilling(ctx, true, &free); !errors.Is(err, core.ErrUnknownTier) {
		t.Errorf("an unknown default tier = %v", err)
	}
	s.opErr = errStore
	if _, err := svc.SetBilling(ctx, true, nil); !errors.Is(err, errStore) ||
		errors.Is(err, core.ErrUnknownTier) {
		t.Errorf("SetBilling with the store down = %v", err)
	}
}

func TestUnitOperatorEntitlement(t *testing.T) {
	ctx := t.Context()
	org := kernel.NewOrganizationID()
	ten := int64(10)
	s := &store{
		entitlement: &featuresapi.Entitlement{
			Tier: "Business", Version: 4,
			Features: []string{"drift-detection", "scans", "stacks"},
			Limits:   map[string]int64{"seats": 10},
		},
		grants: core.Grants{
			Features: []string{"scans"},
			Limits:   map[string]*int64{"seats": &ten},
		},
	}
	// Switching private-worker-pools off takes drift-detection, which
	// requires it, with it.
	svc := core.New(s, inTx(new(int)), []string{"private-worker-pools"})
	s.billingOff = true

	o, err := svc.OrganizationEntitlement(ctx, org)
	if err != nil || o.Tier != "Business" || o.Version != 4 ||
		!slices.Equal(o.Features, []string{"scans", "stacks"}) ||
		!maps.Equal(o.Limits, map[string]int64{"seats": 10}) ||
		!reflect.DeepEqual(o.Grants, s.grants) {
		t.Errorf(
			"OrganizationEntitlement = %+v, %v; want the tier as it applies with billing on",
			o,
			err,
		)
	}

	steps := []struct {
		name string
		do   func() (core.Operated, error)
		call string
	}{
		{
			"tier",
			func() (core.Operated, error) { return svc.SetTier(ctx, org, "Free") },
			"tier Free",
		},
		{
			"grant feature",
			func() (core.Operated, error) { return svc.GrantFeature(ctx, org, "policies") },
			"grant policies",
		},
		{
			"revoke feature",
			func() (core.Operated, error) { return svc.RevokeFeature(ctx, org, "policies") },
			"revoke policies",
		},
		{
			"grant limit",
			func() (core.Operated, error) { return svc.GrantLimit(ctx, org, "seats", &ten) },
			"grant seats",
		},
		{
			"lift limit",
			func() (core.Operated, error) { return svc.GrantLimit(ctx, org, "seats", nil) },
			"grant seats",
		},
		{
			"revoke limit",
			func() (core.Operated, error) { return svc.RevokeLimit(ctx, org, "seats") },
			"revoke seats",
		},
	}
	for _, st := range steps {
		s.calls = nil
		if o, err := st.do(); err != nil || o.Tier != "Business" ||
			!slices.Equal(s.calls, []string{st.call}) {
			t.Errorf("%s = %+v, %v, calls %v", st.name, o, err, s.calls)
		}
	}
	minus := int64(-1)
	for name, err := range map[string]error{
		"a limit as a feature": second(svc.GrantFeature(ctx, org, "seats")),
		"an unknown feature":   second(svc.GrantFeature(ctx, org, "nope")),
		"a feature as a limit": second(svc.GrantLimit(ctx, org, "stacks", &ten)),
		"a negative value":     second(svc.GrantLimit(ctx, org, "seats", &minus)),
	} {
		if !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s = %v", name, err)
		}
	}

	s.opErr = errStore
	for _, st := range steps {
		if _, err := st.do(); !errors.Is(err, errStore) {
			t.Errorf("%s with the store down = %v", st.name, err)
		}
	}
	s.opErr, s.grantsErr = nil, errStore
	if _, err := svc.OrganizationEntitlement(ctx, org); !errors.Is(err, errStore) {
		t.Errorf("grants unreadable = %v", err)
	}
	s.err = errStore
	if _, err := svc.OrganizationEntitlement(ctx, org); !errors.Is(err, errStore) {
		t.Errorf("entitlement unreadable = %v", err)
	}
}
