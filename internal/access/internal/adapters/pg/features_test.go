package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// features is the features domain's rows as the organization delete sees
// them: it records whether its delete ran inside a transaction.
type features struct {
	featuresapi.Entitlements
	deleted []kernel.OrganizationID
	inTx    bool
	err     error
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

// seats is the features domain's limit check as key creation sees it: it
// counts, records what it counted and whether the count ran in a
// transaction, and refuses with refuse when set.
type seats struct {
	featuresapi.Entitlements
	usage  featuresapi.Usage
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
	f.inTx = tx.In(ctx)
	u, err := count(ctx)
	if err != nil {
		return err
	}
	f.usage = u
	return f.refuse
}

// TestAccSeats checks the seats an API key's creation counts: each active
// member once however many accounts admit it, and each live key that is
// not personal and was created or used in the last 30 days.
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
		// seed's key, created now, counts; so does another account's.
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator)
			VALUES ($1, $2, 'xyz', 'SECRET', $3, 'test')`, []any{other, kernel.NewULID(), h}},
		// Created long ago and used lately: counts.
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, created_at, last_used_at)
			VALUES ($1, $2, 'used', 'SECRET', $3, 'test', $4, $5)`, []any{x.acct, kernel.NewULID(), h, old, now}},
		// Created long ago and never used since: does not.
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, created_at, last_used_at)
			VALUES ($1, $2, 'idle', 'SECRET', $3, 'test', $4, $4)`, []any{x.acct, kernel.NewULID(), h, old}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, deleted_at)
			VALUES ($1, $2, 'deleted', 'SECRET', $3, 'test', $4)`, []any{x.acct, kernel.NewULID(), h, now}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, disabled_at)
			VALUES ($1, $2, 'disabled', 'SECRET', $3, 'test', $4)`, []any{x.acct, kernel.NewULID(), h, now}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, expires_at)
			VALUES ($1, $2, 'expired', 'SECRET', $3, 'test', $4)`, []any{x.acct, kernel.NewULID(), h, now}},
		{`INSERT INTO api_keys (account_id, id, name, type, secret_hash, creator, is_personal, owner_user_id)
			VALUES ($1, $2, 'mine', 'SECRET', $3, 'test', true, $4)`, []any{x.acct, kernel.NewULID(), h, x.member}},
	} {
		if _, err := db.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	k := apikey.Key{
		OrganizationID: x.org, AccountID: x.acct, ID: kernel.NewULID(), Name: "new",
		Type: apikey.TypeSecret, SecretHash: h, Creator: "alice", CreatedAt: now,
	}
	if err := s.CreateAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	// One member, in two accounts; seed's key, the other account's and
	// the used one; the account holds all but the other account's key.
	if want := (featuresapi.Usage{Organization: 4, Account: 3}); f.usage != want || !f.inTx {
		t.Errorf("counted %+v, in a transaction %t; want %+v", f.usage, f.inTx, want)
	}

	f.refuse = &featuresapi.LimitError{
		Limit: featuresapi.Seats,
		Scope: featuresapi.ScopeOrganization,
		Value: 5,
	}
	refused := k
	refused.ID = kernel.NewULID()
	if err := s.CreateAPIKey(ctx, refused); !errors.Is(err, f.refuse) {
		t.Errorf("a refused creation = %v", err)
	}
	if _, err := s.AccountAPIKey(ctx, x.acct, refused.ID); err == nil {
		t.Error("a refused key was stored")
	}
}

// TestAccSeatsUnavailable checks that a count the database fails is the
// creation's error.
func TestAccSeatsUnavailable(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "raviga")
	s := pg.New(db).WithFeatures(&seats{})
	h, _ := apikey.Hash(apikey.NewSecret())
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
		return s.CreateAPIKey(ctx, apikey.Key{
			OrganizationID: x.org, AccountID: x.acct, ID: kernel.NewULID(), Name: "new",
			Type: apikey.TypeSecret, SecretHash: h, Creator: "alice", CreatedAt: time.Now(),
		})
	})
	if err == nil || errors.Is(err, port.ErrNotFound) {
		t.Errorf("a count in an aborted transaction = %v", err)
	}
}
