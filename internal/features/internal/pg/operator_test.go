package pg_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	root "github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/features/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func held(t *testing.T, s *pg.Store, name string) core.Held {
	t.Helper()
	ts, err := s.HeldTiers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range ts {
		if h.Name == name {
			return h
		}
	}
	t.Fatalf("no tier %q in %+v", name, ts)
	return core.Held{}
}

func version(t *testing.T, s *pg.Store, org kernel.OrganizationID) int64 {
	t.Helper()
	e, err := s.Entitlement(t.Context(), org, false)
	if err != nil {
		t.Fatal(err)
	}
	return e.Version
}

func TestAccOperatorTiers(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	notified := listen(t, db)

	gold := root.Tier{
		Name: "Gold", Features: []string{"scans", "stacks"},
		Limits: map[string]int64{"seats": 5},
	}
	if err := s.CreateTier(ctx, gold); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTier(ctx, gold); !errors.Is(err, core.ErrExists) {
		t.Errorf("a name in use = %v", err)
	}
	if h := held(t, s, "Gold"); !slices.Equal(h.Features, gold.Features) ||
		!maps.Equal(h.Limits, gold.Limits) || h.Organizations != 0 {
		t.Errorf("created %+v", h)
	}
	if got := notified(); got != nil {
		t.Errorf("a new tier, which no organization holds, notified %v", got)
	}

	org, other := kernel.NewOrganizationID(), kernel.NewOrganizationID()
	entitle(t, db, org, "Gold")
	entitle(t, db, other, tier(t, db, nil, nil))
	gold.Features, gold.Limits = []string{"policies"}, map[string]int64{"private-workers": 1}
	if err := s.ReplaceTier(ctx, gold); err != nil {
		t.Fatal(err)
	}
	if h := held(t, s, "Gold"); !slices.Equal(h.Features, gold.Features) ||
		!maps.Equal(h.Limits, gold.Limits) || h.Organizations != 1 {
		t.Errorf("replaced %+v", h)
	}
	if version(t, s, org) != 1 || version(t, s, other) != 0 {
		t.Errorf("versions %d and %d, want the holder's bumped alone",
			version(t, s, org), version(t, s, other))
	}
	if got := notified(); !slices.Equal(got, []string{core.TiersKey}) {
		t.Errorf("a replaced tier notified %v", got)
	}
	if err := s.ReplaceTier(
		ctx,
		root.Tier{Name: "Silver"},
	); !errors.Is(
		err,
		featuresapi.ErrNotFound,
	) {
		t.Errorf("replacing a tier that does not exist = %v", err)
	}

	if err := s.DeleteTier(ctx, "Gold"); !errors.Is(err, core.ErrInvalidState) {
		t.Errorf("deleting a held tier = %v", err)
	}
	if h := held(t, s, "Gold"); len(h.Features) != 1 {
		t.Errorf("a refused delete left %+v", h)
	}
	spare := tier(t, db, []string{"stacks"}, map[string]int64{"seats": 1})
	if err := s.SetBilling(ctx, false, &spare); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTier(ctx, spare); !errors.Is(err, core.ErrInvalidState) {
		t.Errorf("deleting the default tier = %v", err)
	}
	unused := tier(t, db, []string{"stacks"}, map[string]int64{"seats": 1})
	exec(t, db, `INSERT INTO tier_seeds (tier, features, limits) VALUES ($1, '{}', '{}')`, unused)
	notified()
	if err := s.DeleteTier(ctx, unused); err != nil {
		t.Fatal(err)
	}
	var seeds int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM tier_seeds WHERE tier = $1`, unused).
		Scan(&seeds); err != nil || seeds != 1 {
		t.Errorf("the deleted tier's seed row: %d, %v; want it kept", seeds, err)
	}
	if got := notified(); !slices.Equal(got, []string{core.TiersKey}) {
		t.Errorf("a deleted tier notified %v", got)
	}
	if err := s.DeleteTier(ctx, unused); !errors.Is(err, featuresapi.ErrNotFound) {
		t.Errorf("deleting it again = %v", err)
	}
}

func TestAccOperatorBilling(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	notified := listen(t, db)
	if b, err := s.BillingState(ctx); err != nil || b.Enabled || b.DefaultTier != root.Enterprise {
		t.Errorf("BillingState = %+v, %v; want off on Enterprise", b, err)
	}
	gold := tier(t, db, nil, nil)
	if err := s.SetBilling(ctx, true, &gold); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBilling(ctx, true, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := s.BillingState(ctx); err != nil || !b.Enabled || b.DefaultTier != gold {
		t.Errorf("BillingState = %+v, %v", b, err)
	}
	if got := notified(); !slices.Equal(got, []string{core.BillingKey, core.BillingKey}) {
		t.Errorf("SetBilling notified %v", got)
	}
	missing := "Missing"
	if err := s.SetBilling(ctx, false, &missing); !errors.Is(err, featuresapi.ErrNotFound) {
		t.Errorf("an unknown default tier = %v", err)
	}
	if b, _ := s.BillingState(ctx); !b.Enabled {
		t.Error("a refused SetBilling changed the switch")
	}
}

func TestAccOperatorGrants(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	org := kernel.NewOrganizationID()
	five := int64(5)
	for name, err := range map[string]error{
		"grants":         second(s.Grants(ctx, org)),
		"tier":           s.SetTier(ctx, org, "Free"),
		"grant feature":  s.GrantFeature(ctx, org, "stacks"),
		"revoke feature": s.RevokeFeature(ctx, org, "stacks"),
		"grant limit":    s.GrantLimit(ctx, org, "seats", &five),
		"revoke limit":   s.RevokeLimit(ctx, org, "seats"),
	} {
		if !errors.Is(err, featuresapi.ErrNotFound) {
			t.Errorf("%s of an organization without an entitlement = %v", name, err)
		}
	}

	starter := tier(t, db, []string{"stacks"}, map[string]int64{"seats": 2})
	entitle(t, db, org, tier(t, db, nil, nil))
	notified := listen(t, db)
	if err := s.SetTier(ctx, org, "No such tier"); !errors.Is(err, core.ErrUnknownTier) {
		t.Errorf("an unknown tier = %v", err)
	}
	for _, step := range []error{
		s.SetTier(ctx, org, starter),
		s.GrantFeature(ctx, org, "scans"),
		s.GrantFeature(ctx, org, "scans"),
		s.GrantLimit(ctx, org, "seats", &five),
		s.GrantLimit(ctx, org, "private-workers", nil),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}
	g, err := s.Grants(ctx, org)
	if err != nil || !slices.Equal(g.Features, []string{"scans"}) || len(g.Limits) != 2 ||
		*g.Limits["seats"] != 5 || g.Limits["private-workers"] != nil {
		t.Errorf("Grants = %+v, %v", g, err)
	}
	e, err := s.Entitlement(ctx, org, false)
	if err != nil || e.Tier != starter || e.Version != 5 ||
		!slices.Equal(e.Features, []string{"scans", "stacks"}) ||
		!maps.Equal(e.Limits, map[string]int64{"seats": 5}) {
		t.Errorf("Entitlement = %+v, %v", e, err)
	}
	key := core.InvalidateKey(org)
	if got := notified(); !slices.Equal(got, []string{key, key, key, key, key}) {
		t.Errorf("five writes notified %v", got)
	}

	for _, step := range []error{s.RevokeFeature(ctx, org, "scans"), s.RevokeLimit(ctx, org, "seats")} {
		if step != nil {
			t.Fatal(step)
		}
	}
	for name, err := range map[string]error{
		"feature": s.RevokeFeature(ctx, org, "scans"),
		"limit":   s.RevokeLimit(ctx, org, "seats"),
	} {
		if !errors.Is(err, featuresapi.ErrNotFound) {
			t.Errorf("revoking a %s not granted = %v", name, err)
		}
	}
	if v := version(t, s, org); v != 7 {
		t.Errorf("version %d after two revokes and two refused, want 7", v)
	}
}

func TestAccOperatorStoreUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	org := kernel.NewOrganizationID()
	five := int64(5)
	for name, err := range map[string]error{
		"held":    second(s.HeldTiers(ctx)),
		"create":  s.CreateTier(ctx, root.Tier{Name: "Gold"}),
		"replace": s.ReplaceTier(ctx, root.Tier{Name: "Gold"}),
		"delete":  s.DeleteTier(ctx, "Gold"),
		"billing": second(s.BillingState(ctx)),
		"set":     s.SetBilling(ctx, true, nil),
		"grants":  second(s.Grants(ctx, org)),
		"tier":    s.SetTier(ctx, org, "Gold"),
		"grant":   s.GrantLimit(ctx, org, "seats", &five),
	} {
		if err == nil || errors.Is(err, featuresapi.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }
