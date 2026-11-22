package pg_test

import (
	"context"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

func TestAccClaimStackSkipsAMutationAndKeepsAccountIsolation(t *testing.T) {
	db := pool(t)
	ctx := t.Context()
	account, id, space := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	if _, err := db.Exec(ctx, `INSERT INTO stacks (account_id,id,slug,space_id,name,repository,branch,provider,vendor)
		VALUES ($1,$2,'claim-target',$3,'Claim target','infra','main','GITHUB','terraform')`, account, id, space); err != nil {
		t.Fatal(err)
	}
	service := core.New(core.Deps{Store: pg.New(db)})
	holder, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	var locked kernel.ULID
	if err := holder.QueryRow(ctx, `SELECT id FROM stacks WHERE account_id=$1 AND id=$2 FOR UPDATE`, account, id).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	check := func(want bool, acct kernel.AccountID) {
		t.Helper()
		if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
			settings, held, err := service.TryLockForRun(ctx, acct, id)
			if err != nil || held != want || held && (settings.ID != id || settings.Space != space) {
				t.Fatalf("claim stack fence: held=%t want=%t settings=%+v err=%v", held, want, settings, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check(false, account)
	check(false, kernel.NewAccountID())
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	check(true, account)
}
