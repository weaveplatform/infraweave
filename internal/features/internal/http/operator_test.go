package http

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/features"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/features/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

// operated returns the partial over a seeded database, with the
// organization "acme" on the default tier, and the pool.
func operated(t *testing.T) (OperatorServer, *pgxpool.Pool, kernel.OrganizationID) {
	t.Helper()
	svc, db, acme := seeded(t)
	orgs := func(_ context.Context, slug string) (kernel.OrganizationID, bool, error) {
		switch slug {
		case "acme":
			return acme, true, nil
		case "down":
			return kernel.OrganizationID{}, false, errDown
		}
		return kernel.OrganizationID{}, false, nil
	}
	return NewOperatorServer(svc, orgs), db, acme
}

// seeded returns the service over a seeded database, with the
// organization "acme" on the default tier, and the pool.
func seeded(t *testing.T) (*core.Service, *pgxpool.Pool, kernel.OrganizationID) {
	t.Helper()
	ctx := t.Context()
	db, err := pgxpool.New(ctx, sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	inTx := func(ctx context.Context, fn func(context.Context) error) error {
		return tx.Do(ctx, db, tx.Options{}, fn)
	}
	svc := core.New(pg.New(db), inTx, nil)
	if err := svc.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	acme := kernel.NewOrganizationID()
	if err := svc.CreateOrganization(ctx, acme); err != nil {
		t.Fatal(err)
	}
	return svc, db, acme
}

// wantType checks that err is a problem of typ.
func wantType(t *testing.T, what string, err error, typ problem.Type) {
	t.Helper()
	pe, ok := errors.AsType[*problem.Error](err)
	if !ok || pe.Problem.Type != typ.New("").Type {
		t.Errorf("%s = %v, want %s", what, err, typ.New("").Type)
	}
}

func TestAccOperatorTiers(t *testing.T) {
	ctx := t.Context()
	s, _, _ := operated(t)

	two := int32(2)
	r, err := s.Tiers(ctx, opapi.TiersRequestObject{Params: opapi.TiersParams{Limit: &two}})
	first, ok := r.(opapi.Tiers200JSONResponse)
	if err != nil || !ok || len(first.Body.Items) != 2 || first.Body.NextCursor == nil ||
		first.Body.Items[0].Name != features.Business || first.Body.Items[1].Name != features.Enterprise ||
		first.Body.Items[1].Editable || first.Body.Items[1].Organizations != 1 {
		t.Fatalf("first page %+v, %v", r, err)
	}
	r, err = s.Tiers(
		ctx,
		opapi.TiersRequestObject{Params: opapi.TiersParams{Cursor: first.Body.NextCursor}},
	)
	rest, ok := r.(opapi.Tiers200JSONResponse)
	if err != nil || !ok || len(rest.Body.Items) != 3 || rest.Body.NextCursor != nil ||
		rest.Body.Items[0].Name != features.Free {
		t.Errorf("second page %+v, %v", r, err)
	}
	bad := "nope"
	_, err = s.Tiers(ctx, opapi.TiersRequestObject{Params: opapi.TiersParams{Cursor: &bad}})
	wantType(t, "a bad cursor", err, problem.InvalidCursor)

	created, err := s.TierCreate(ctx, opapi.TierCreateRequestObject{Body: &opapi.TierInput{
		Name: "Gold", Features: []string{"stacks"}, Limits: map[string]int64{"seats": 4},
	}})
	if c, ok := created.(opapi.TierCreate201JSONResponse); err != nil || !ok ||
		c.Body.Name != "Gold" ||
		!c.Body.Editable ||
		c.Body.Limits["seats"] != 4 {
		t.Errorf("TierCreate = %+v, %v", created, err)
	}
	_, err = s.TierCreate(ctx, opapi.TierCreateRequestObject{Body: &opapi.TierInput{Name: "Gold"}})
	wantType(t, "a name in use", err, problem.AlreadyExists)
	_, err = s.TierCreate(ctx, opapi.TierCreateRequestObject{Body: &opapi.TierInput{
		Name: "Silver", Features: []string{"seats"},
	}})
	wantType(t, "a limit as a feature", err, problem.ValidationFailed)

	got, err := s.Tier(ctx, opapi.TierRequestObject{Tier: "Gold"})
	if g, ok := got.(opapi.Tier200JSONResponse); err != nil || !ok ||
		!slices.Equal(g.Body.Features, []string{"stacks"}) {
		t.Errorf("Tier = %+v, %v", got, err)
	}
	_, err = s.Tier(ctx, opapi.TierRequestObject{Tier: "Silver"})
	wantType(t, "an unknown tier", err, problem.NotFound)

	updated, err := s.TierUpdate(ctx, opapi.TierUpdateRequestObject{
		Tier: "Gold",
		Body: &opapi.TierContent{Features: []string{"scans"}},
	})
	if u, ok := updated.(opapi.TierUpdate200JSONResponse); err != nil || !ok ||
		!slices.Equal(u.Body.Features, []string{"scans"}) || len(u.Body.Limits) != 0 {
		t.Errorf("TierUpdate = %+v, %v", updated, err)
	}
	_, err = s.TierUpdate(ctx, opapi.TierUpdateRequestObject{
		Tier: features.Enterprise,
		Body: &opapi.TierContent{},
	})
	wantType(t, "editing Enterprise", err, problem.InvalidState)

	_, err = s.TierDelete(ctx, opapi.TierDeleteRequestObject{Tier: features.Enterprise})
	wantType(t, "deleting Enterprise", err, problem.InvalidState)
	deleted, err := s.TierDelete(ctx, opapi.TierDeleteRequestObject{Tier: "Gold"})
	if d, ok := deleted.(opapi.TierDelete200JSONResponse); err != nil || !ok ||
		d.Body.Name != "Gold" {
		t.Errorf("TierDelete = %+v, %v", deleted, err)
	}
}

func TestAccOperatorBillingAndGrants(t *testing.T) {
	ctx := t.Context()
	s, db, _ := operated(t)

	b, err := s.Billing(ctx, opapi.BillingRequestObject{})
	if g, ok := b.(opapi.Billing200JSONResponse); err != nil || !ok || g.Body.Enabled ||
		g.Body.DefaultTier != features.Enterprise {
		t.Errorf("Billing = %+v, %v", b, err)
	}
	starter := features.Starter
	set, err := s.BillingSet(ctx, opapi.BillingSetRequestObject{Body: &opapi.BillingInput{
		Enabled: true, DefaultTier: &starter,
	}})
	if g, ok := set.(opapi.BillingSet200JSONResponse); err != nil || !ok || !g.Body.Enabled ||
		g.Body.DefaultTier != starter {
		t.Errorf("BillingSet = %+v, %v", set, err)
	}
	missing := "Missing"
	_, err = s.BillingSet(
		ctx,
		opapi.BillingSetRequestObject{Body: &opapi.BillingInput{DefaultTier: &missing}},
	)
	wantType(t, "an unknown default tier", err, problem.ValidationFailed)

	e, err := s.OrganizationEntitlement(
		ctx,
		opapi.OrganizationEntitlementRequestObject{Organization: "acme"},
	)
	if g, ok := e.(opapi.OrganizationEntitlement200JSONResponse); err != nil || !ok ||
		g.Body.Tier != features.Enterprise || len(g.Body.Limits) != 0 ||
		g.Body.Grants.Features == nil || g.Body.Grants.Limits == nil {
		t.Errorf("OrganizationEntitlement = %+v, %v", e, err)
	}
	_, err = s.OrganizationEntitlement(
		ctx,
		opapi.OrganizationEntitlementRequestObject{Organization: "zzz"},
	)
	wantType(t, "an unknown organization", err, problem.NotFound)
	if _, err := s.OrganizationEntitlement(
		ctx,
		opapi.OrganizationEntitlementRequestObject{
			Organization: "down",
		},
	); !errors.Is(err, errDown) {
		t.Errorf("an organization lookup that failed = %v", err)
	}

	tier, err := s.OrganizationTierSet(ctx, opapi.OrganizationTierSetRequestObject{
		Organization: "acme", Body: &opapi.TierChoice{Tier: features.Free},
	})
	if g, ok := tier.(opapi.OrganizationTierSet200JSONResponse); err != nil || !ok ||
		g.Body.Tier != features.Free || g.Body.Version != 1 || slices.Contains(g.Body.Features, "policies") {
		t.Errorf("OrganizationTierSet = %+v, %v", tier, err)
	}
	_, err = s.OrganizationTierSet(ctx, opapi.OrganizationTierSetRequestObject{
		Organization: "acme", Body: &opapi.TierChoice{Tier: "Missing"},
	})
	wantType(t, "an unknown tier", err, problem.ValidationFailed)

	grant, err := s.OrganizationFeatureGrant(ctx, opapi.OrganizationFeatureGrantRequestObject{
		Organization: "acme", Feature: "policies",
	})
	if g, ok := grant.(opapi.OrganizationFeatureGrant200JSONResponse); err != nil || !ok ||
		!slices.Contains(g.Body.Features, "policies") ||
		!slices.Equal(g.Body.Grants.Features, []string{"policies"}) {
		t.Errorf("OrganizationFeatureGrant = %+v, %v", grant, err)
	}
	if _, err := s.OrganizationFeatureRevoke(ctx, opapi.OrganizationFeatureRevokeRequestObject{
		Organization: "acme", Feature: "policies",
	}); err != nil {
		t.Error(err)
	}
	_, err = s.OrganizationFeatureRevoke(ctx, opapi.OrganizationFeatureRevokeRequestObject{
		Organization: "acme", Feature: "policies",
	})
	wantType(t, "revoking a feature not granted", err, problem.NotFound)
	_, err = s.OrganizationFeatureGrant(ctx, opapi.OrganizationFeatureGrantRequestObject{
		Organization: "acme", Feature: "seats",
	})
	wantType(t, "granting a limit as a feature", err, problem.ValidationFailed)

	nine := int64(9)
	limit, err := s.OrganizationLimitGrant(ctx, opapi.OrganizationLimitGrantRequestObject{
		Organization: "acme", Limit: "seats", Body: &opapi.LimitGrantInput{Value: &nine},
	})
	if g, ok := limit.(opapi.OrganizationLimitGrant200JSONResponse); err != nil || !ok ||
		g.Body.Limits["seats"] != 9 || len(g.Body.Grants.Limits) != 1 ||
		*g.Body.Grants.Limits[0].Value != 9 {
		t.Errorf("OrganizationLimitGrant = %+v, %v", limit, err)
	}
	if _, err := s.OrganizationLimitRevoke(ctx, opapi.OrganizationLimitRevokeRequestObject{
		Organization: "acme", Limit: "seats",
	}); err != nil {
		t.Error(err)
	}
	_, err = s.OrganizationLimitRevoke(ctx, opapi.OrganizationLimitRevokeRequestObject{
		Organization: "acme", Limit: "seats",
	})
	wantType(t, "revoking a limit not granted", err, problem.NotFound)

	db.Close()
	for name, err := range map[string]error{
		"tiers":   second(s.Tiers(ctx, opapi.TiersRequestObject{})),
		"billing": second(s.Billing(ctx, opapi.BillingRequestObject{})),
		"set": second(s.BillingSet(ctx, opapi.BillingSetRequestObject{
			Body: &opapi.BillingInput{Enabled: true},
		})),
		"entitlement": second(s.OrganizationEntitlement(ctx,
			opapi.OrganizationEntitlementRequestObject{Organization: "acme"})),
	} {
		if _, ok := errors.AsType[*problem.Error](err); err == nil || ok {
			t.Errorf("%s on a closed pool = %v, want the store's error", name, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }
