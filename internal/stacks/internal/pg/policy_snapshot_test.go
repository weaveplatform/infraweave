package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestAccPolicySnapshotUsesOwnedStateAndLabels(t *testing.T) {
	f := newFixture(t)
	in := input("Policy facts")
	in.Labels = []string{"prod", "owner:policy"}
	stack, err := f.svc.Create(t.Context(), f.admin, "", false, "", in)
	if err != nil {
		t.Fatal(err)
	}
	read := func(state kernel.StackState, name string, labels []string) {
		t.Helper()
		got, err := f.svc.PolicySnapshot(t.Context(), f.acct, stack.ID)
		if err != nil || got.Settings.ID != stack.ID || got.Settings.Name != name || got.State != state || !slices.Equal(got.Labels, labels) {
			t.Fatalf("policy snapshot=%+v error=%v", got, err)
		}
	}
	read(kernel.StackStateNone, "Policy facts", []string{"owner:policy", "prod"})
	abort := errors.New("rollback state projection")
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		if err := f.svc.SetRunState(ctx, f.acct, stack.ID, kernel.StackState(kernel.RunStateApplying), true); err != nil {
			return err
		}
		got, err := f.svc.PolicySnapshot(ctx, f.acct, stack.ID)
		if err != nil {
			return err
		}
		if got.State != kernel.StackState(kernel.RunStateApplying) {
			return errors.New("policy reader missed the caller's projected state")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	read(kernel.StackStateNone, "Policy facts", []string{"owner:policy", "prod"})
	in.Name, in.Labels = "Updated facts", []string{"changed"}
	if _, err := f.svc.Update(t.Context(), f.admin, stack.Slug, kernel.Expect{}, in); err != nil {
		t.Fatal(err)
	}
	read(kernel.StackStateNone, "Updated facts", []string{"changed"})
	for _, tc := range []struct {
		name    string
		account kernel.AccountID
		id      kernel.ULID
	}{
		{"foreign account", kernel.NewAccountID(), stack.ID},
		{"missing stack", f.acct, kernel.NewULID()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.PolicySnapshot(t.Context(), tc.account, tc.id); !errors.Is(err, stacksapi.ErrNotFound) {
				t.Fatalf("policy snapshot error=%v, want not found", err)
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
		_, err := f.svc.PolicySnapshot(ctx, f.acct, stack.ID)
		return err
	})
	if !errors.Is(err, kernel.ErrUnavailable) || errors.Is(err, stacksapi.ErrNotFound) {
		t.Fatalf("terminated backend error=%v, want operational unavailability", err)
	}
}
