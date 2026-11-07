package assembly

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) {
	os.Exit(sqltest.Main(m, schema.Stream(), Stream()))
}

// TestAccDomain checks the domain's entitlements end to end: features.disabled
// leaves the entitlement, and a limit check runs in its own transaction.
func TestAccDomain(t *testing.T) {
	ctx := t.Context()
	db, err := pgxpool.New(ctx, sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	for _, stmt := range []string{
		`INSERT INTO tiers (name) VALUES ('Starter')`,
		`INSERT INTO tier_features (tier, feature) VALUES ('Starter', 'scans'), ('Starter', 'stacks')`,
		`INSERT INTO tier_limits (tier, limit_name, value) VALUES ('Starter', 'seats', 0)`,
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	e := New(Deps{
		DB:       db,
		Settings: Settings{Disabled: []string{"scans"}},
	}).Entitlements()
	org := kernel.NewOrganizationID()
	if err := e.CreateOrganization(ctx, org, "Starter"); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Entitlement(
		ctx,
		org,
	); err != nil ||
		!slices.Equal(got.Features, []string{"stacks"}) {
		t.Errorf("Entitlement = %+v, %v", got, err)
	}
	err = e.CheckLimit(ctx, org, kernel.NewAccountID(), "seats",
		func(context.Context) (featuresapi.Usage, error) { return featuresapi.Usage{}, nil })
	if le, ok := errors.AsType[*featuresapi.LimitError](err); !ok || le.Value != 0 {
		t.Errorf("CheckLimit on a pool of none = %v", err)
	}
}
