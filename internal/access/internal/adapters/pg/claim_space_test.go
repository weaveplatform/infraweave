package pg_test

import (
	"context"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccClaimSpaceSkipsAMutationAndKeepsAccountIsolation(t *testing.T) {
	x := seed(t, pool(t), "claim-space-owner")
	ctx := t.Context()
	holder, err := x.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	var id kernel.ULID
	if err := holder.QueryRow(ctx, `SELECT id FROM spaces WHERE account_id=$1 AND id=$1 FOR UPDATE`, x.acct).Scan(&id); err != nil {
		t.Fatal(err)
	}
	spaces := treeOf(x.s).svc
	check := func(want bool, account kernel.AccountID, target kernel.ULID) {
		t.Helper()
		if err := tx.Do(ctx, x.db, tx.Options{}, func(ctx context.Context) error {
			held, err := spaces.TryHold(ctx, account, target)
			if err != nil || held != want {
				t.Fatalf("claim space fence: held=%t want=%t err=%v", held, want, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check(false, x.acct, x.acct.ULID)
	check(false, kernel.NewAccountID(), x.acct.ULID)
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	check(true, x.acct, x.acct.ULID)
}
