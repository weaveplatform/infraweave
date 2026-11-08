package pg_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// TestMain migrates the platform stream and the stacks stream alone: no
// stacks table references another domain's.
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

// TestAccStore reads a stack and the stacks in a space of one account,
// and none of another's.
func TestAccStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	space, id := kernel.NewULID(), kernel.NewULID()
	if _, err := db.Exec(ctx, `INSERT INTO stacks (account_id, id, slug, space_id)
		VALUES ($1, $2, 'infra', $3)`, acct, id, space); err != nil {
		t.Fatal(err)
	}
	want := stacksapi.Stack{ID: id, Account: acct, Slug: "infra", Space: space}
	if got, err := s.Get(ctx, acct, id); err != nil || got != want {
		t.Errorf("Get = %+v, %v; want %+v", got, err, want)
	}
	err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		_, err := s.Get(ctx, acct, id)
		return err
	})
	if err != nil {
		t.Errorf("Get in a transaction = %v", err)
	}
	for name, err := range map[string]error{
		"a missing stack":       func() error { _, err := s.Get(ctx, acct, kernel.NewULID()); return err }(),
		"another account's one": func() error { _, err := s.Get(ctx, other, id); return err }(),
	} {
		if !errors.Is(err, stacksapi.ErrNotFound) {
			t.Errorf("Get of %s = %v, want ErrNotFound", name, err)
		}
	}
	for _, c := range []struct {
		name  string
		acct  kernel.AccountID
		space kernel.ULID
		want  bool
	}{
		{"the stack's space", acct, space, true},
		{"another space", acct, kernel.NewULID(), false},
		{"another account's space of that ID", other, space, false},
	} {
		if got, err := s.SpaceInUse(ctx, c.acct, c.space); err != nil || got != c.want {
			t.Errorf("SpaceInUse of %s = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

// TestAccStoreUnavailable checks that a read on a closed pool fails, and
// not as a missing stack.
func TestAccStoreUnavailable(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	db.Close()
	ctx := context.Background()
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	if _, err := s.Get(ctx, acct, id); err == nil || errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("Get on a closed pool = %v", err)
	}
	if _, err := s.SpaceInUse(ctx, acct, id); err == nil {
		t.Error("SpaceInUse on a closed pool succeeded")
	}
}
