package pg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestAccPushPolicyLockOwnershipAndAtomicity(t *testing.T) {
	f := newFixture(t)
	stack, err := f.svc.Create(t.Context(), f.admin, "", false, "", input("Policy lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.UpdatePushLock(t.Context(), f.acct, stack.ID, "deploy", ""); !errors.Is(err, stacksapi.ErrPolicyLockTransaction) {
		t.Fatalf("unfenced policy lock error=%v", err)
	}
	write := func(lock, unlock string) error {
		return tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
			return f.svc.UpdatePushLock(ctx, f.acct, stack.ID, lock, unlock)
		})
	}
	read := func() stacksapi.PolicySnapshot {
		t.Helper()
		snapshot, err := f.svc.PolicySnapshot(t.Context(), f.acct, stack.ID)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	if err := write("deploy", ""); err != nil {
		t.Fatal(err)
	}
	first := read()
	if first.Settings.Lock == nil || first.Settings.Lock.Holder != stacksapi.PolicyLockPrefix+"deploy" || first.Settings.Lock.By != "deploy" {
		t.Fatalf("policy owner lock=%+v", first.Settings.Lock)
	}
	for _, keys := range [][2]string{{"deploy", ""}, {"", "other"}, {"", ""}} {
		if err := write(keys[0], keys[1]); err != nil {
			t.Fatal(err)
		}
		got := read()
		if got.Settings.Version != first.Settings.Version || got.Settings.Lock == nil || *got.Settings.Lock != *first.Settings.Lock {
			t.Fatal("an idempotent or unrelated key changed the owned lock")
		}
	}
	if err := write("other", "wrong"); !errors.Is(err, stacksapi.ErrPolicyLockConflict) {
		t.Fatalf("conflicting owner accepted: %v", err)
	}
	abort := errors.New("rollback policy lock")
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		if err := f.svc.UpdatePushLock(ctx, f.acct, stack.ID, "replacement", "deploy"); err != nil {
			return err
		}
		snapshot, err := f.svc.PolicySnapshot(ctx, f.acct, stack.ID)
		if err != nil {
			return err
		}
		if snapshot.Settings.Lock == nil || snapshot.Settings.Lock.Holder != stacksapi.PolicyLockPrefix+"replacement" {
			return fmt.Errorf("lock write did not join its caller's transaction")
		}
		return abort
	})
	if !errors.Is(err, abort) || read().Settings.Lock.Holder != first.Settings.Lock.Holder {
		t.Fatalf("policy lock escaped rollback: %v", err)
	}
	if err := write("replacement", "deploy"); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Settings.Lock == nil || got.Settings.Lock.Holder != stacksapi.PolicyLockPrefix+"replacement" || got.Settings.Version != first.Settings.Version+1 {
		t.Fatalf("atomic unlock/lock changed ownership incorrectly: %+v", got.Settings.Lock)
	}
	if err := write("", "replacement"); err != nil || read().Settings.Lock != nil {
		t.Fatalf("matching policy unlock=%v", err)
	}
	human, err := f.svc.Lock(t.Context(), f.admin, stack.Slug, "human-owned")
	if err != nil {
		t.Fatal(err)
	}
	if err := write("", human.Lock.Holder); err != nil || read().Settings.Lock == nil || read().Settings.Lock.Holder != human.Lock.Holder {
		t.Fatalf("a policy released a human lock: %v", err)
	}
	if err := write(human.Lock.Holder, human.Lock.Holder); !errors.Is(err, stacksapi.ErrPolicyLockConflict) {
		t.Fatalf("a policy impersonated a human owner: %v", err)
	}
	for _, scope := range []struct {
		account kernel.AccountID
		id      kernel.ULID
		want    error
	}{
		{kernel.AccountID{}, stack.ID, stacksapi.ErrPolicyLockInput},
		{f.acct, kernel.ULID{}, stacksapi.ErrPolicyLockInput},
		{kernel.NewAccountID(), stack.ID, stacksapi.ErrNotFound},
		{f.acct, kernel.NewULID(), stacksapi.ErrNotFound},
	} {
		err := tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
			return f.svc.UpdatePushLock(ctx, scope.account, scope.id, "x", "")
		})
		if !errors.Is(err, scope.want) {
			t.Fatalf("scope refusal=%v want=%v", err, scope.want)
		}
	}
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		var pid int32
		if err := tx.Q(ctx, f.db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		if _, err := f.db.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			return err
		}
		return f.svc.UpdatePushLock(ctx, f.acct, stack.ID, "x", "")
	})
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("terminated policy-lock backend error=%v", err)
	}
}
