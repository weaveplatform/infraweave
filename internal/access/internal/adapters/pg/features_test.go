package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// entitled is the features domain as a fake embeds it for the methods it
// does not record: every organization's entitlement is created, and any
// other call panics on the nil interface.
type entitled struct{ featuresapi.Entitlements }

func (entitled) CreateOrganization(context.Context, kernel.OrganizationID) error { return nil }

// features is the features domain's rows as the organization create and
// delete see them: it records each, whether it ran inside a transaction,
// and fails the create with createErr and the delete with err.
type features struct {
	entitled
	created   []kernel.OrganizationID
	createdTx bool
	createErr error
	deleted   []kernel.OrganizationID
	inTx      bool
	err       error
}

func (f *features) CreateOrganization(ctx context.Context, org kernel.OrganizationID) error {
	f.created = append(f.created, org)
	f.createdTx = tx.In(ctx)
	return f.createErr
}

// TestAccCreateOrganizationFeatures checks that creating an organization
// creates its entitlement in the same transaction, and that a failure
// there creates nothing.
func TestAccCreateOrganizationFeatures(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	f := &features{createErr: errors.New("features down")}
	s := pg.New(db).WithFeatures(f)
	o, a := tenant(t, "hooli", "hooli")
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); !errors.Is(err, f.createErr) {
		t.Fatalf("a failed entitlement = %v", err)
	}
	if _, err := s.Organization(ctx, o.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("the organization after a failed entitlement = %v", err)
	}
	f.createErr = nil
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 2 || f.created[1] != o.ID || !f.createdTx {
		t.Errorf("entitlements created = %v, in a transaction %t", f.created, f.createdTx)
	}
}

func (f *features) DeleteOrganization(ctx context.Context, org kernel.OrganizationID) error {
	f.deleted = append(f.deleted, org)
	f.inTx = tx.In(ctx)
	return f.err
}

// TestAccDeleteOrganizationFeatures checks that deleting an organization
// deletes its features rows in the same transaction, and that a failure
// there keeps the organization.
func TestAccDeleteOrganizationFeatures(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	f := &features{err: errors.New("features down")}
	s := pg.New(db).WithFeatures(f)
	o, a := tenant(t, "pied", "pied")
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteOrganization(ctx, o.ID, created(o)); !errors.Is(err, f.err) {
		t.Fatalf("a failed features delete = %v", err)
	}
	if _, err := s.Organization(ctx, o.ID); err != nil {
		t.Errorf("the organization after a failed features delete = %v", err)
	}
	f.err = nil
	if _, err := s.DeleteOrganization(ctx, o.ID, created(o)); err != nil {
		t.Fatal(err)
	}
	if len(f.deleted) != 2 || f.deleted[1] != o.ID || !f.inTx {
		t.Errorf("features deletes = %v, in a transaction %t", f.deleted, f.inTx)
	}
}

// seats is the features domain's limit check as a key's first use sees
// it: it counts, records what it counted, how often it was called and
// whether it ran in a transaction, and refuses with refuse when set.
type seats struct {
	entitled
	usage  featuresapi.Usage
	calls  int
	inTx   bool
	refuse error
}

func (f *seats) CheckLimit(
	ctx context.Context,
	_ kernel.OrganizationID,
	_ kernel.AccountID,
	limit string,
	count func(context.Context) (featuresapi.Usage, error),
) error {
	if limit != featuresapi.Seats {
		return errors.New("checked " + limit)
	}
	f.calls++
	f.inTx = tx.In(ctx)
	u, err := count(ctx)
	if err != nil {
		return err
	}
	f.usage = u
	return f.refuse
}

// TestAccSeats checks Spacelift's seat rule at a key's use: a key takes a
// seat on its first use in 30 days, which is checked, and holds it without
// a check while used; the count is each active member once however many
// accounts admit it, and each other live key that is not personal and was
// used in the window.
func TestAccSeats(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "hooli")
	f := &seats{}
	s := pg.New(db).WithFeatures(f)
	now := time.Now().UTC()
	old := now.Add(-pg.SeatWindow - time.Hour)
	other := kernel.NewAccountID()
	retired := kernel.NewMemberID()
	personal, fresh := kernel.NewULID(), kernel.NewULID()
	h, _ := apikey.Hash(apikey.NewSecret())
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{
			`INSERT INTO accounts (organization_id, id, slug, name) VALUES ($1, $2, 'hooli-xyz', 'XYZ')`,
			[]any{x.org, other},
		},
		{`INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ($1, $2, 'gavin', 'Gavin', 'gavin@example.com', 'deactivated')`, []any{x.org, retired}},
		{`INSERT INTO account_users (account_id, organization_id, member_id)
			VALUES ($1, $3, $4), ($2, $3, $4), ($1, $3, $5)`, []any{x.acct, other, x.org, x.member, retired}},
		// Used lately: counts, here and in the other account.
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, last_used_at)
			VALUES ($1, $2, 'xyz', 'SECRET', $3, 'test', $4)`, []any{other, kernel.NewULID(), h, now}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, created_at, last_used_at)
			VALUES ($1, $2, 'used', 'SECRET', $3, 'test', $4, $5)`, []any{x.acct, kernel.NewULID(), h, old, now}},
		// Used, but not in the window; never used; gone: none counts.
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, last_used_at)
			VALUES ($1, $2, 'idle', 'SECRET', $3, 'test', $4)`, []any{x.acct, kernel.NewULID(), h, old}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator)
			VALUES ($1, $2, 'fresh', 'SECRET', $3, 'test')`, []any{x.acct, fresh, h}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, last_used_at, deleted_at)
			VALUES ($1, $2, 'deleted', 'SECRET', $3, 'test', $4, $4)`, []any{x.acct, kernel.NewULID(), h, now}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, last_used_at, disabled_at)
			VALUES ($1, $2, 'disabled', 'SECRET', $3, 'test', $4, $4)`, []any{x.acct, kernel.NewULID(), h, now}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, last_used_at, expires_at)
			VALUES ($1, $2, 'expired', 'SECRET', $3, 'test', $4, $4)`, []any{x.acct, kernel.NewULID(), h, now}},
		{
			`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, is_personal,
			owner_user_id, last_used_at) VALUES ($1, $2, 'mine', 'SECRET', $3, 'test', true, $4, $5)`,
			[]any{x.acct, personal, h, x.member, now},
		},
	} {
		if _, err := db.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	used := func(id kernel.ULID) bool {
		t.Helper()
		var at *time.Time
		if err := db.QueryRow(ctx, `SELECT last_used_at FROM api_keys WHERE id = $1`, id).
			Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at != nil
	}

	// seed's key, never used, takes a seat on its first use.
	if err := s.TouchAPIKey(ctx, x.key); err != nil || !used(x.key) {
		t.Fatalf("the first use = %v", err)
	}
	// One member in two accounts; the other account's key and the used one.
	if want := (featuresapi.Usage{Organization: 3, Account: 2}); f.calls != 1 || f.usage != want ||
		!f.inTx {
		t.Errorf(
			"checked %d times, counted %+v, in a transaction %t; want once, %+v",
			f.calls,
			f.usage,
			f.inTx,
			want,
		)
	}
	// The seats read leaves no key out, so seed's key now counts too.
	if u, err := s.Seats(ctx, x.org, x.acct); err != nil ||
		u != (featuresapi.Usage{Organization: 4, Account: 3}) {
		t.Errorf("Seats = %+v, %v", u, err)
	}
	// A key that holds its seat, and a personal key, are not checked.
	for _, id := range []kernel.ULID{x.key, personal} {
		if err := s.TouchAPIKey(ctx, id); err != nil || f.calls != 1 {
			t.Errorf("a later use of %v = %v, %d checks", id, err, f.calls)
		}
	}

	f.refuse = &featuresapi.LimitError{
		Limit: featuresapi.Seats,
		Scope: featuresapi.ScopeOrganization,
		Value: 4,
	}
	if err := s.TouchAPIKey(ctx, fresh); !errors.Is(err, f.refuse) || used(fresh) {
		t.Errorf("a refused first use = %v, recorded %t", err, used(fresh))
	}
	if err := s.TouchAPIKey(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown key = %v", err)
	}
	if err := pg.New(db).TouchAPIKey(ctx, fresh); err != nil || !used(fresh) {
		t.Errorf("a use without the features domain = %v", err)
	}
}

// TestAccSeatsUnavailable checks that a seat check the database fails is
// the use's error.
func TestAccSeatsUnavailable(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "raviga")
	s := pg.New(db).WithFeatures(&seats{})
	for name, store := range map[string]*pg.Store{"checked": s, "unchecked": pg.New(db)} {
		err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
			_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
			return store.TouchAPIKey(ctx, x.key)
		})
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("a %s use in an aborted transaction = %v", name, err)
		}
	}
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
		_, err := s.Seats(ctx, x.org, x.acct)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "count seats") {
		t.Errorf("a seats read in an aborted transaction = %v", err)
	}
	// The key is read, then the count fails.
	s = pg.New(db).WithFeatures(failingCount{db: db})
	if err := s.TouchAPIKey(
		ctx,
		x.key,
	); err == nil ||
		!strings.Contains(err.Error(), "count seats") {
		t.Errorf("a failed count = %v", err)
	}
}

// failingCount aborts the use's transaction before it counts.
type failingCount struct {
	entitled
	db *pgxpool.Pool
}

func (f failingCount) CheckLimit(
	ctx context.Context,
	_ kernel.OrganizationID,
	_ kernel.AccountID,
	_ string,
	count func(context.Context) (featuresapi.Usage, error),
) error {
	_, _ = tx.Q(ctx, f.db).Exec(ctx, `SELECT 1/0`)
	_, err := count(ctx)
	return err
}
