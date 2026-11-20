package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccPolicySpaceReadsActualAccountOwnedLabels(t *testing.T) {
	db := pool(t)
	x := seed(t, db, "policyspace")
	owned, err := x.s.Space(t.Context(), x.acct, "policyspace")
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string, labels []string) {
		t.Helper()
		got, err := x.s.PolicySpace(t.Context(), x.acct, owned.ID)
		if err != nil || got.ID != owned.ID || got.Slug != owned.Slug || got.Name != name || !slices.Equal(got.Labels, labels) {
			t.Fatalf("policy space=%+v error=%v", got, err)
		}
		raw, err := json.Marshal(got)
		if err != nil || strings.Contains(string(raw), "unconsumed-space-description") {
			t.Fatalf("unconsumed space values reached metadata: %s error=%v", raw, err)
		}
	}
	read(owned.Name, []string{})
	if _, err := db.Exec(t.Context(), `UPDATE spaces SET description='unconsumed-space-description',labels=ARRAY['production','region:eu']
		WHERE account_id=$1 AND id=$2`, x.acct, owned.ID); err != nil {
		t.Fatal(err)
	}
	read(owned.Name, []string{"production", "region:eu"})
	abort := errors.New("rollback space metadata")
	err = tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, db).Exec(ctx, `UPDATE spaces SET name='Uncommitted',labels=ARRAY['uncommitted'] WHERE account_id=$1 AND id=$2`, x.acct, owned.ID); err != nil {
			return err
		}
		got, err := x.s.PolicySpace(ctx, x.acct, owned.ID)
		if err != nil {
			return err
		}
		if got.Name != "Uncommitted" || !slices.Equal(got.Labels, []string{"uncommitted"}) {
			return errors.New("space metadata reader did not join its caller's transaction")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	read(owned.Name, []string{"production", "region:eu"})
	for _, scope := range []struct {
		name    string
		account kernel.AccountID
		space   kernel.ULID
	}{{"foreign account", kernel.NewAccountID(), owned.ID}, {"missing space", x.acct, kernel.NewULID()}} {
		t.Run(scope.name, func(t *testing.T) {
			if _, err := x.s.PolicySpace(t.Context(), scope.account, scope.space); !errors.Is(err, accessapi.ErrNoSpace) {
				t.Fatalf("policy space error=%v, want no space", err)
			}
		})
	}
	err = tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		var pid int32
		if err := tx.Q(ctx, db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			return err
		}
		_, err := x.s.PolicySpace(ctx, x.acct, owned.ID)
		return err
	})
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("terminated space backend error=%v", err)
	}
}
