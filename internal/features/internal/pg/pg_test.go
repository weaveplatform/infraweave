package pg_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/features"
	"github.com/weaveplatform/infraweave/internal/features/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func exec(t *testing.T, db *pgxpool.Pool, stmt string, args ...any) {
	t.Helper()
	if _, err := db.Exec(t.Context(), stmt, args...); err != nil {
		t.Fatal(err)
	}
}

// tier creates a tier named after t with features and limits.
func tier(t *testing.T, db *pgxpool.Pool, features []string, limits map[string]int64) string {
	t.Helper()
	name := "tier-" + kernel.NewULID().String()
	exec(t, db, `INSERT INTO tiers (name) VALUES ($1)`, name)
	for _, f := range features {
		exec(t, db, `INSERT INTO tier_features (tier, feature) VALUES ($1, $2)`, name, f)
	}
	for _, l := range slices.Sorted(maps.Keys(limits)) {
		exec(t, db, `INSERT INTO tier_limits (tier, limit_name, value) VALUES ($1, $2, $3)`,
			name, l, limits[l])
	}
	return name
}

func TestAccEntitlement(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	business := tier(t, db, []string{"stacks", "scans"},
		map[string]int64{"seats": 10, "private-workers": 3, "ai-budget": 100})
	org := kernel.NewOrganizationID()

	if _, err := s.Entitlement(ctx, org, false); !errors.Is(err, featuresapi.ErrNotFound) {
		t.Errorf("no entitlement = %v", err)
	}
	if err := s.CreateOrganization(
		ctx,
		org,
		"no-such-tier",
	); !errors.Is(
		err,
		featuresapi.ErrNotFound,
	) {
		t.Errorf("an unknown tier = %v", err)
	}
	if err := s.CreateOrganization(ctx, org, business); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO organization_feature_grants (organization_id, feature)
		VALUES ($1, 'drift-detection'), ($1, 'stacks')`, org)
	// A grant above the tier, a grant lifting a limit, and a limit only a
	// grant sets.
	exec(t, db, `INSERT INTO organization_limit_grants (organization_id, limit_name, value)
		VALUES ($1, 'seats', 25), ($1, 'ai-budget', NULL), ($1, 'public-worker-slots', 2)`, org)
	e, err := s.Entitlement(ctx, org, false)
	want := map[string]int64{"seats": 25, "private-workers": 3, "public-worker-slots": 2}
	if err != nil || e.Tier != business || e.Version != 0 ||
		!slices.Equal(e.Features, []string{"drift-detection", "scans", "stacks"}) ||
		!maps.Equal(e.Limits, want) {
		t.Errorf("Entitlement = %+v, %v", e, err)
	}

	bare := kernel.NewOrganizationID()
	if err := s.CreateOrganization(ctx, bare, tier(t, db, nil, nil)); err != nil {
		t.Fatal(err)
	}
	if e, err := s.Entitlement(ctx, bare, true); err != nil || len(e.Features) != 0 ||
		len(e.Limits) != 0 {
		t.Errorf("an empty tier's entitlement = %+v, %v", e, err)
	}
}

func TestAccAccountLimits(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	org, acct, other := kernel.NewOrganizationID(), kernel.NewAccountID(), kernel.NewAccountID()
	if err := s.SetAccountLimit(
		ctx,
		org,
		acct,
		"seats",
		2,
	); !errors.Is(
		err,
		featuresapi.ErrNotFound,
	) {
		t.Errorf("a limit of an organization without an entitlement = %v", err)
	}
	if err := s.CreateOrganization(ctx, org, tier(t, db, nil, nil)); err != nil {
		t.Fatal(err)
	}
	for _, l := range []struct {
		acct  kernel.AccountID
		value int64
	}{{acct, 2}, {acct, 5}, {other, 1}} {
		if err := s.SetAccountLimit(ctx, org, l.acct, "seats", l.value); err != nil {
			t.Fatal(err)
		}
	}
	if v, ok, err := s.AccountLimit(ctx, acct, "seats"); err != nil || !ok || v != 5 {
		t.Errorf("AccountLimit = %d, %t, %v", v, ok, err)
	}
	if _, ok, err := s.AccountLimit(ctx, acct, "private-workers"); err != nil || ok {
		t.Errorf("an unset limit = %t, %v", ok, err)
	}
	if err := s.DeleteAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.AccountLimit(ctx, acct, "seats"); err != nil || ok {
		t.Errorf("after DeleteAccount = %t, %v", ok, err)
	}
	if v, ok, err := s.AccountLimit(ctx, other, "seats"); err != nil || !ok || v != 1 {
		t.Errorf("another account after DeleteAccount = %d, %t, %v", v, ok, err)
	}
}

// TestAccDeleteOrganization checks that an organization's rows go with it,
// another's stay, and a delete in a rolled-back transaction leaves them.
func TestAccDeleteOrganization(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	name := tier(t, db, []string{"stacks"}, nil)
	org, kept := kernel.NewOrganizationID(), kernel.NewOrganizationID()
	for _, o := range []kernel.OrganizationID{org, kept} {
		if err := s.CreateOrganization(ctx, o, name); err != nil {
			t.Fatal(err)
		}
		exec(t, db, `INSERT INTO organization_feature_grants (organization_id, feature)
			VALUES ($1, 'scans')`, o)
		exec(t, db, `INSERT INTO organization_limit_grants (organization_id, limit_name, value)
			VALUES ($1, 'seats', 1)`, o)
		if err := s.SetAccountLimit(ctx, o, kernel.NewAccountID(), "seats", 1); err != nil {
			t.Fatal(err)
		}
	}

	errRollback := errors.New("rollback")
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		if err := s.DeleteOrganization(ctx, org); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	if _, err := s.Entitlement(ctx, org, false); err != nil {
		t.Errorf("after a rolled-back delete = %v", err)
	}

	if err := s.DeleteOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Entitlement(ctx, org, false); !errors.Is(err, featuresapi.ErrNotFound) {
		t.Errorf("after a delete = %v", err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM organization_feature_grants WHERE organization_id = $1) +
		(SELECT count(*) FROM organization_limit_grants WHERE organization_id = $1) +
		(SELECT count(*) FROM account_limits WHERE organization_id = $1)`, org).Scan(&n); err != nil ||
		n != 0 {
		t.Errorf("rows left after a delete = %d, %v", n, err)
	}
	if e, err := s.Entitlement(ctx, kept, false); err != nil ||
		!slices.Equal(e.Features, []string{"scans", "stacks"}) {
		t.Errorf("another organization after a delete = %+v, %v", e, err)
	}
	if err := s.DeleteOrganization(ctx, org); err != nil {
		t.Errorf("deleting an organization without rows = %v", err)
	}
}

// TestAccCreateRolledBack checks that an entitlement created in a
// transaction that rolls back leaves no row.
func TestAccCreateRolledBack(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	org := kernel.NewOrganizationID()
	name := tier(t, db, nil, nil)
	errRollback := errors.New("rollback")
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		if err := s.CreateOrganization(ctx, org, name); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	if _, err := s.Entitlement(ctx, org, false); !errors.Is(err, featuresapi.ErrNotFound) {
		t.Errorf("after a rolled-back create = %v", err)
	}
}

// TestAccLimitCheckSerializes races creations for the last seat of a pool
// of one: each checks the limit and inserts its seat in one transaction,
// so the entitlement row's lock lets exactly one through.
func TestAccLimitCheckSerializes(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	exec(
		t,
		db,
		`CREATE TABLE seats_taken (organization_id uuid NOT NULL, account_id uuid NOT NULL)`,
	)
	org := kernel.NewOrganizationID()
	store := pg.New(db)
	if err := store.CreateOrganization(
		ctx,
		org,
		tier(t, db, nil, map[string]int64{"seats": 1}),
	); err != nil {
		t.Fatal(err)
	}
	inTx := func(ctx context.Context, fn func(context.Context) error) error {
		return tx.Do(ctx, db, tx.Options{}, fn)
	}
	svc := features.New(store, inTx, nil)

	const racers = 4
	var (
		start = make(chan struct{})
		wg    sync.WaitGroup
		errs  = make([]error, racers)
	)
	for i := range racers {
		wg.Go(func() {
			<-start
			acct := kernel.NewAccountID()
			errs[i] = inTx(ctx, func(ctx context.Context) error {
				err := svc.CheckLimit(ctx, org, acct, "seats",
					func(ctx context.Context) (featuresapi.Usage, error) {
						var u featuresapi.Usage
						err := tx.Q(ctx, db).QueryRow(ctx, `SELECT count(*),
							count(*) FILTER (WHERE account_id = $2)
							FROM seats_taken WHERE organization_id = $1`, org, acct).
							Scan(&u.Organization, &u.Account)
						return u, err
					})
				if err != nil {
					return err
				}
				_, err = tx.Q(ctx, db).Exec(ctx,
					`INSERT INTO seats_taken (organization_id, account_id) VALUES ($1, $2)`, org, acct)
				return err
			})
		})
	}
	close(start)
	wg.Wait()
	var admitted, refused int
	for _, err := range errs {
		le, ok := errors.AsType[*featuresapi.LimitError](err)
		switch {
		case err == nil:
			admitted++
		case ok && le.Scope == featuresapi.ScopeOrganization && le.Value == 1:
			refused++
		default:
			t.Errorf("a racer failed: %v", err)
		}
	}
	if admitted != 1 || refused != racers-1 {
		t.Errorf("%d admitted and %d refused, want 1 and %d", admitted, refused, racers-1)
	}
}

func TestAccStoreUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	for name, err := range map[string]error{
		"create":         s.CreateOrganization(ctx, org, "Enterprise"),
		"delete":         s.DeleteOrganization(ctx, org),
		"set":            s.SetAccountLimit(ctx, org, acct, "seats", 1),
		"delete account": s.DeleteAccount(ctx, acct),
		"entitlement": func() error {
			_, err := s.Entitlement(ctx, org, false)
			return err
		}(),
		"account limit": func() error {
			_, _, err := s.AccountLimit(ctx, acct, "seats")
			return err
		}(),
	} {
		if err == nil || errors.Is(err, featuresapi.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestAccDeleteAborted runs the organization delete in a transaction a
// failed statement aborted.
func TestAccDeleteAborted(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	errAborted := errors.New("aborted")
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
		if err := s.DeleteOrganization(ctx, kernel.NewOrganizationID()); err == nil {
			t.Error("a delete in an aborted transaction succeeded")
		}
		return errAborted
	})
	if !errors.Is(err, errAborted) {
		t.Fatal(err)
	}
}
