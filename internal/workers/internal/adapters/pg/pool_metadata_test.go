package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

func TestAccPolicyPoolMetadataIsCurrentAndAccountOwned(t *testing.T) {
	f := newFixture(t)
	created, err := f.svc.Create(t.Context(), f.admin, pool.Input{Name: "Builders", Space: "team", Labels: []string{"prod"}}, f.csr)
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string, labels []string) {
		t.Helper()
		got, err := f.svc.PoolMetadata(t.Context(), f.acct, created.ID)
		if err != nil || got.ID != created.ID || got.Space != f.team || got.Name != name || got.Public || !slices.Equal(got.Labels, labels) {
			t.Fatalf("pool policy metadata=%+v error=%v", got, err)
		}
	}
	read("Builders", []string{"prod"})
	if _, err := f.svc.Update(t.Context(), f.admin, created.ID.String(), kernel.Expect{}, pool.Input{Name: "Updated", Space: "team", Labels: []string{"changed"}}); err != nil {
		t.Fatal(err)
	}
	read("Updated", []string{"changed"})
	abort := errors.New("rollback pool metadata")
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, f.db).Exec(ctx, "UPDATE worker_pools SET name='uncommitted' WHERE account_id=$1 AND id=$2", f.acct, created.ID); err != nil {
			return err
		}
		got, err := f.svc.PoolMetadata(ctx, f.acct, created.ID)
		if err != nil {
			return err
		}
		if got.Name != "uncommitted" {
			return errors.New("pool reader missed its caller's update")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	read("Updated", []string{"changed"})
	for _, tc := range []struct {
		name    string
		account kernel.AccountID
		id      kernel.ULID
	}{
		{"foreign account", kernel.NewAccountID(), created.ID},
		{"missing pool", f.acct, kernel.NewULID()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.PoolMetadata(t.Context(), tc.account, tc.id); !errors.Is(err, workersapi.ErrNoPool) {
				t.Fatalf("pool metadata error=%v, want no pool", err)
			}
		})
	}
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		var pid int32
		if err := tx.Q(ctx, f.db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		if _, err := f.db.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			return err
		}
		_, err := f.svc.PoolMetadata(ctx, f.acct, created.ID)
		return err
	})
	if !errors.Is(err, kernel.ErrUnavailable) || errors.Is(err, workersapi.ErrNoPool) {
		t.Fatalf("terminated backend error=%v, want operational unavailability", err)
	}
}
