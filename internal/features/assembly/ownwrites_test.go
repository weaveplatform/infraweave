package assembly

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	entitlement "github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// TestAccReadOwnWrites checks, on one replica whose infraweave_invalidate
// notifications never arrive, since nothing listens, that each write of
// what its snapshot holds reads back at once: the read before the write
// fills the snapshot, and the read after it answers the write.
func TestAccReadOwnWrites(t *testing.T) {
	ctx := t.Context()
	db, err := pgxpool.New(ctx, sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	d := New(Deps{DB: db})
	if err := d.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	svc, e := d.entitlements, d.Entitlements()
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	if err := e.CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	free, _ := features.Seed(features.Free)
	seeded := free.Features[0]
	custom := features.Tier{Name: "Custom", Features: []string{"policies"}, Limits: map[string]int64{}}
	seats := int64(7)
	for _, step := range []struct {
		name string
		// read answers what the step's write changes, through the
		// replica's snapshot.
		read  func() any
		write func() error
		want  any
	}{
		{
			name: "SetBilling", read: billing(ctx, t, svc),
			write: func() error { _, err := svc.SetBilling(ctx, true, nil); return err },
			want:  true,
		},
		{
			name: "SetTier", read: tier(ctx, t, e, org),
			write: func() error { _, err := svc.SetTier(ctx, org, features.Free); return err },
			want:  features.Free,
		},
		{
			name: "Seed", read: holds(ctx, t, e, org, seeded),
			write: func() error {
				// A release whose seed of Free adds a feature the last
				// seed lacked, and which the operator has not edited.
				for _, stmt := range []string{
					`DELETE FROM tier_features WHERE tier = $1 AND feature = $2`,
					`UPDATE tier_seeds SET features = array_remove(features, $2) WHERE tier = $1`,
				} {
					if _, err := db.Exec(ctx, stmt, features.Free, seeded); err != nil {
						return err
					}
				}
				svc.Flush()
				if got := holds(ctx, t, e, org, seeded)(); got != false {
					t.Fatalf("Free before the release holds %s: %v", seeded, got)
				}
				return d.Seed(ctx)
			},
			want: true,
		},
		{
			name: "CreateTier", read: holding(ctx, t, svc, org, "policies"),
			write: func() error { _, err := svc.CreateTier(ctx, custom); return err },
			want:  []string{"Custom", features.Starter, features.Business, features.Enterprise},
		},
		{
			name: "ReplaceTier", read: holds(ctx, t, e, org, "mfa"),
			write: func() error {
				if _, err := svc.SetTier(ctx, org, custom.Name); err != nil {
					return err
				}
				_ = holds(ctx, t, e, org, "mfa")()
				custom.Features = append(custom.Features, "mfa")
				_, err := svc.ReplaceTier(ctx, custom)
				return err
			},
			want: true,
		},
		{
			name: "GrantFeature", read: holds(ctx, t, e, org, "oidc-sso"),
			write: func() error { _, err := svc.GrantFeature(ctx, org, "oidc-sso"); return err },
			want:  true,
		},
		{
			name: "RevokeFeature", read: holds(ctx, t, e, org, "oidc-sso"),
			write: func() error { _, err := svc.RevokeFeature(ctx, org, "oidc-sso"); return err },
			want:  false,
		},
		{
			name: "GrantLimit", read: limit(ctx, t, e, org, "seats"),
			write: func() error { _, err := svc.GrantLimit(ctx, org, "seats", &seats); return err },
			want:  seats,
		},
		{
			name: "RevokeLimit", read: limit(ctx, t, e, org, "seats"),
			write: func() error { _, err := svc.RevokeLimit(ctx, org, "seats"); return err },
			want:  int64(-1),
		},
		{
			name: "SetTier back", read: tier(ctx, t, e, org),
			write: func() error { _, err := svc.SetTier(ctx, org, features.Free); return err },
			want:  features.Free,
		},
		{
			name: "DeleteTier", read: holding(ctx, t, svc, org, "policies"),
			write: func() error { _, err := svc.DeleteTier(ctx, custom.Name); return err },
			want:  []string{features.Starter, features.Business, features.Enterprise},
		},
		{
			name: "SetFeaturePreviewsEnabled", read: optedIn(ctx, t, e, acct),
			write: func() error { return e.SetFeaturePreviewsEnabled(ctx, acct, true) },
			want:  true,
		},
		{
			name: "DeleteAccount", read: optedIn(ctx, t, e, acct),
			write: func() error { return d.Purge(ctx, org, acct, false) },
			want:  false,
		},
		{
			name: "DeleteOrganization", read: tier(ctx, t, e, org),
			write: func() error { return d.Purge(ctx, org, acct, true) },
			want:  "",
		},
	} {
		// Each step's snapshot starts from storage, so a step passes only
		// on its own write's drop, not on one an earlier step left.
		svc.Flush()
		before := step.read()
		if err := step.write(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := step.read(); !equal(got, step.want) {
			t.Errorf("%s: read %v after the write, want %v (before it %v)",
				step.name, got, step.want, before)
		}
	}
}

// equal compares two of a step's reads.
func equal(a, b any) bool {
	as, aok := a.([]string)
	bs, bok := b.([]string)
	if aok || bok {
		return slices.Equal(as, bs)
	}
	return a == b
}

func billing(ctx context.Context, t *testing.T, svc *entitlement.Service) func() any {
	return func() any {
		t.Helper()
		on, err := svc.Billing(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return on
	}
}

// tier reads org's tier from its entitlement, "" once it has none.
func tier(ctx context.Context, t *testing.T, e featuresapi.Entitlements, org kernel.OrganizationID) func() any {
	return func() any {
		t.Helper()
		got, err := e.Entitlement(ctx, org)
		if errors.Is(err, featuresapi.ErrNotFound) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return got.Tier
	}
}

func holds(ctx context.Context, t *testing.T, e featuresapi.Entitlements, org kernel.OrganizationID, feature string) func() any {
	return func() any {
		t.Helper()
		got, err := e.Entitlement(ctx, org)
		if err != nil {
			t.Fatal(err)
		}
		return slices.Contains(got.Features, feature)
	}
}

// limit reads org's value of name, -1 when it has none.
func limit(ctx context.Context, t *testing.T, e featuresapi.Entitlements, org kernel.OrganizationID, name string) func() any {
	return func() any {
		t.Helper()
		got, err := e.Entitlement(ctx, org)
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := got.Limits[name]; ok {
			return v
		}
		return int64(-1)
	}
}

// holding reads the tiers a refusal of feature to org names, from the
// replica's snapshot of the tiers.
func holding(ctx context.Context, t *testing.T, svc *entitlement.Service, org kernel.OrganizationID, feature string) func() any {
	return func() any {
		t.Helper()
		err := svc.AllowAccount(ctx, org, kernel.AccountID{}, feature)
		r, ok := errors.AsType[*entitlement.Refusal](err)
		if !ok {
			t.Fatalf("AllowAccount(%s) = %v, want a refusal", feature, err)
		}
		return r.Tiers
	}
}

func optedIn(ctx context.Context, t *testing.T, e featuresapi.Entitlements, acct kernel.AccountID) func() any {
	return func() any {
		t.Helper()
		on, err := e.FeaturePreviewsEnabled(ctx, acct)
		if err != nil {
			t.Fatal(err)
		}
		return on
	}
}
