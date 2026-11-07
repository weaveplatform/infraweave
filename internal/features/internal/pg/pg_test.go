package pg_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	root "github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/features"
	"github.com/weaveplatform/infraweave/internal/features/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
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

// entitle gives org its entitlement on tier directly, as the default tier
// is the operator's to choose.
func entitle(t *testing.T, db *pgxpool.Pool, org kernel.OrganizationID, tier string) {
	t.Helper()
	exec(t, db, `INSERT INTO organization_entitlements (organization_id, tier) VALUES ($1, $2)`,
		org, tier)
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
	// Enterprise, the default while the operator names none, is not seeded
	// here.
	if err := s.CreateOrganization(ctx, org); !errors.Is(err, featuresapi.ErrNotFound) {
		t.Errorf("a default tier that does not exist = %v", err)
	}
	if err := s.SetBilling(ctx, false, &business); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOrganization(ctx, org); err != nil {
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
	entitle(t, db, bare, tier(t, db, nil, nil))
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
	entitle(t, db, org, tier(t, db, nil, nil))
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
	if err := s.SetAccountLimit(ctx, org, acct, "private-workers", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AccountLimits(ctx, acct); err != nil ||
		!reflect.DeepEqual(got, map[string]int64{"seats": 5, "private-workers": 1}) {
		t.Errorf("AccountLimits = %v, %v", got, err)
	}
	if err := s.ClearAccountLimit(ctx, acct, "private-workers"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearAccountLimit(ctx, acct, "private-workers"); err != nil {
		t.Errorf("clearing a limit again = %v", err)
	}
	if err := s.DeleteAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AccountLimits(ctx, acct); err != nil || len(got) != 0 {
		t.Errorf("after DeleteAccount = %v, %v", got, err)
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
		entitle(t, db, o, name)
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
	if err := s.SetBilling(ctx, false, &name); err != nil {
		t.Fatal(err)
	}
	errRollback := errors.New("rollback")
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		if err := s.CreateOrganization(ctx, org); err != nil {
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
	entitle(t, db, org, tier(t, db, nil, map[string]int64{"seats": 1}))
	inTx := func(ctx context.Context, fn func(context.Context) error) error {
		return tx.Do(ctx, db, tx.Options{}, fn)
	}
	svc := features.New(store, inTx, nil)
	exec(t, db, `UPDATE billing SET enabled = true`)

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
		"create":         s.CreateOrganization(ctx, org),
		"delete":         s.DeleteOrganization(ctx, org),
		"set":            s.SetAccountLimit(ctx, org, acct, "seats", 1),
		"delete account": s.DeleteAccount(ctx, acct),
		"clear":          s.ClearAccountLimit(ctx, acct, "seats"),
		"account limits": func() error {
			_, err := s.AccountLimits(ctx, acct)
			return err
		}(),
		"entitlement": func() error {
			_, err := s.Entitlement(ctx, org, false)
			return err
		}(),
		"account limit": func() error {
			_, _, err := s.AccountLimit(ctx, acct, "seats")
			return err
		}(),
		"billing": func() error {
			_, err := s.Billing(ctx)
			return err
		}(),
		"tiers": func() error {
			_, err := s.Tiers(ctx)
			return err
		}(),
		"seed": s.Seed(ctx, nil),
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

// seedTiers reads back the tiers whose names are in names.
func seedTiers(t *testing.T, s *pg.Store, names ...string) map[string]root.Tier {
	t.Helper()
	all, err := s.Tiers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]root.Tier{}
	for _, tr := range all {
		if slices.Contains(names, tr.Name) {
			out[tr.Name] = tr
		}
	}
	return out
}

// listen returns a connection listening on infraweave_invalidate, and a
// func that sends a marker and returns the payloads that arrived before it,
// so a test sees every notification a step sent without waiting on a clock.
func listen(t *testing.T, db *pgxpool.Pool) func() []string {
	t.Helper()
	conn, err := db.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)
	if _, err := conn.Exec(t.Context(), "LISTEN "+notify.Invalidate); err != nil {
		t.Fatal(err)
	}
	return func() []string {
		t.Helper()
		exec(t, db, `SELECT pg_notify($1, 'marker')`, notify.Invalidate)
		var got []string
		for {
			n, err := conn.Conn().WaitForNotification(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if n.Payload == "marker" {
				return got
			}
			got = append(got, n.Payload)
		}
	}
}

// TestAccSeed checks decision 0021's seeding: a seeded tier is installed
// once, and a release replaces its content only while the operator has
// not edited or deleted it, nor made a tier of its name first.
func TestAccSeed(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	notified := listen(t, db)
	free := root.Tier{
		Name:     "Free",
		Features: []string{"stacks"},
		Limits:   map[string]int64{"seats": 2},
	}
	paid := root.Tier{
		Name:     "Paid",
		Features: []string{"policies", "stacks"},
		Limits:   map[string]int64{},
	}
	if err := s.Seed(ctx, []root.Tier{free, paid}); err != nil {
		t.Fatal(err)
	}
	if got := seedTiers(
		t,
		s,
		"Free",
		"Paid",
	); !reflect.DeepEqual(
		got,
		map[string]root.Tier{"Free": free, "Paid": paid},
	) {
		t.Errorf("first seed installed %+v", got)
	}
	if got := notified(); !slices.Equal(got, []string{features.TiersKey}) {
		t.Errorf("first seed notified %v", got)
	}

	if err := s.Seed(ctx, []root.Tier{free, paid}); err != nil {
		t.Fatal(err)
	}
	if got := notified(); got != nil {
		t.Errorf("an unchanged seed notified %v", got)
	}

	// The operator edits Paid and deletes Free; the next release changes
	// both, and adds Own, which the operator made first.
	exec(t, db, `INSERT INTO tier_features (tier, feature) VALUES ('Paid', 'scans')`)
	exec(t, db, `DELETE FROM tier_limits WHERE tier = 'Free'`)
	exec(t, db, `DELETE FROM tier_features WHERE tier = 'Free'`)
	exec(t, db, `DELETE FROM tiers WHERE name = 'Free'`)
	own := tier(t, db, []string{"stacks"}, nil)
	edited := seedTiers(t, s, "Paid")["Paid"]
	next := []root.Tier{
		{
			Name:     "Free",
			Features: []string{"policies", "stacks"},
			Limits:   map[string]int64{"seats": 3},
		},
		{Name: "Paid", Features: []string{"stacks"}, Limits: map[string]int64{"seats": 9}},
		{Name: own, Features: []string{"audit-trail"}, Limits: map[string]int64{}},
	}
	if err := s.Seed(ctx, next); err != nil {
		t.Fatal(err)
	}
	got := seedTiers(t, s, "Free", "Paid", own)
	if _, ok := got["Free"]; ok || !reflect.DeepEqual(got["Paid"], edited) ||
		!slices.Equal(got[own].Features, []string{"stacks"}) {
		t.Errorf("a release over the operator's changes left %+v", got)
	}
	if got := notified(); got != nil {
		t.Errorf("a seed that changed nothing notified %v", got)
	}

	// An unedited seeded tier follows the release.
	exec(t, db, `DELETE FROM tier_features WHERE tier = 'Paid' AND feature = 'scans'`)
	if err := s.Seed(ctx, next[1:2]); err != nil {
		t.Fatal(err)
	}
	if got := seedTiers(t, s, "Paid")["Paid"]; !reflect.DeepEqual(got, next[1]) {
		t.Errorf("an unedited tier after a release = %+v, want %+v", got, next[1])
	}
	if got := notified(); !slices.Equal(got, []string{features.TiersKey}) {
		t.Errorf("a release notified %v", got)
	}
}

func TestAccSeedFailures(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	bad := root.Tier{Name: "Bad", Features: []string{}, Limits: map[string]int64{"seats": -1}}
	if err := s.Seed(ctx, []root.Tier{bad}); err == nil {
		t.Error("a seed with a negative limit succeeded")
	}
	if got := seedTiers(t, s, "Bad"); len(got) != 0 {
		t.Errorf("a failed seed left %+v", got)
	}
	errAborted := errors.New("aborted")
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
		if err := s.Seed(ctx, nil); err == nil {
			t.Error("a seed in an aborted transaction succeeded")
		}
		return errAborted
	})
	if !errors.Is(err, errAborted) {
		t.Fatal(err)
	}
}

func TestAccBilling(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	if on, err := s.Billing(t.Context()); err != nil || on {
		t.Errorf("Billing on a new installation = %t, %v", on, err)
	}
	exec(t, db, `UPDATE billing SET enabled = true`)
	if on, err := s.Billing(t.Context()); err != nil || !on {
		t.Errorf("Billing switched on = %t, %v", on, err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO billing (id) VALUES (false)`); err == nil {
		t.Error("a second billing row was accepted")
	}
}

// TestAccByteOrder checks that names read in byte order, as Go compares
// them, whatever the database's collation: "module-sharing" before
// "modules", and "Gold Plus" before "Gold-Plus" before "GoldPlus".
func TestAccByteOrder(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	named := []string{"GoldPlus", "Gold-Plus", "Gold Plus", root.Enterprise}
	seeds := make([]root.Tier, 0, len(named))
	for _, n := range named {
		seeds = append(seeds, root.Tier{
			Name: n, Features: []string{"modules", "module-sharing"}, Limits: map[string]int64{},
		})
	}
	if err := s.Seed(ctx, seeds); err != nil {
		t.Fatal(err)
	}
	ts, err := s.Tiers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(ts))
	for _, tier := range ts {
		names = append(names, tier.Name)
		if !slices.IsSorted(tier.Features) {
			t.Errorf("%s's features read as %v", tier.Name, tier.Features)
		}
	}
	if !slices.IsSorted(names) || len(names) != len(named) {
		t.Errorf("the tiers read as %v", names)
	}
	org := kernel.NewOrganizationID()
	if err := s.CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"modules", "module-sharing"} {
		if err := s.GrantFeature(ctx, org, f); err != nil {
			t.Fatal(err)
		}
	}
	if g, err := s.Grants(ctx, org); err != nil || !slices.IsSorted(g.Features) {
		t.Errorf("the grants read as %v, %v", g.Features, err)
	}
}
