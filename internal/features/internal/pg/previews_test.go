package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/features/internal/pg"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// TestAccPreviewStore checks the feature preview settings on PostgreSQL:
// an account has not opted in and a member has not opted out until they
// say so, an account's write tells every replica on commit, a member's
// removal deletes their setting, and an account's deletion leaves none of
// its rows and no other account's.
//
// ref: decision 0021
func TestAccPreviewStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	heard := listen(t, db)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	alice, bob := kernel.NewMemberID(), kernel.NewMemberID()
	count := func(table string, acct kernel.AccountID) int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE account_id = $1`, acct).
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if on, err := s.FeaturePreviews(ctx, acct); err != nil || on {
		t.Errorf("an account that never set it: %t, %v", on, err)
	}
	for _, want := range []bool{true, true, false} {
		if err := s.SetFeaturePreviews(ctx, acct, want); err != nil {
			t.Fatal(err)
		}
		if on, err := s.FeaturePreviews(ctx, acct); err != nil || on != want {
			t.Errorf("after setting %t: %t, %v", want, on, err)
		}
	}
	if got := heard(); !slices.Equal(got, []string{
		core.AccountKey(acct), core.AccountKey(acct), core.AccountKey(acct),
	}) {
		t.Errorf("notified %q", got)
	}
	if err := s.SetFeaturePreviews(ctx, other, true); err != nil {
		t.Fatal(err)
	}

	// A write rolled back with its transaction tells no replica.
	errRollback := errors.New("rolled back")
	if err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
		if err := s.SetFeaturePreviews(ctx, acct, true); err != nil {
			t.Fatal(err)
		}
		return errRollback
	}); !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	if on, err := s.FeaturePreviews(ctx, acct); err != nil || on {
		t.Errorf("after a rolled-back write: %t, %v", on, err)
	}
	if got := heard(); !slices.Equal(got, []string{core.AccountKey(other)}) {
		t.Errorf("notified %q", got)
	}

	for _, m := range []kernel.MemberID{alice, bob} {
		if on, err := s.PersonalFeaturePreviews(ctx, acct, m); err != nil || !on {
			t.Errorf("a member who never opted out: %t, %v", on, err)
		}
		for _, enabled := range []bool{false, false} {
			if err := s.SetPersonalFeaturePreviews(ctx, acct, m, enabled); err != nil {
				t.Fatal(err)
			}
		}
		if on, err := s.PersonalFeaturePreviews(ctx, acct, m); err != nil || on {
			t.Errorf("a member who opted out: %t, %v", on, err)
		}
	}
	if err := s.SetPersonalFeaturePreviews(ctx, other, alice, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersonalFeaturePreviews(ctx, acct, alice, true); err != nil {
		t.Fatal(err)
	}
	if on, err := s.PersonalFeaturePreviews(ctx, acct, alice); err != nil || !on ||
		count("member_feature_preview_opt_outs", acct) != 1 {
		t.Errorf("a member who opted back in: %t, %v, with %d opt-outs left",
			on, err, count("member_feature_preview_opt_outs", acct))
	}
	if err := s.RemoveMember(ctx, acct, bob); err != nil {
		t.Fatal(err)
	}
	if n := count("member_feature_preview_opt_outs", acct); n != 0 {
		t.Errorf("a removed member leaves %d opt-outs", n)
	}
	if err := s.SetPersonalFeaturePreviews(ctx, acct, bob, false); err != nil {
		t.Fatal(err)
	}
	heard()

	if err := s.DeleteAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if a, m := count("account_feature_previews", acct),
		count("member_feature_preview_opt_outs", acct); a+m != 0 {
		t.Errorf("a deleted account leaves %d opt-ins and %d opt-outs", a, m)
	}
	if a, m := count("account_feature_previews", other),
		count("member_feature_preview_opt_outs", other); a != 1 || m != 1 {
		t.Errorf("another account keeps %d opt-ins and %d opt-outs, want 1 and 1", a, m)
	}
	if got := heard(); !slices.Equal(got, []string{core.AccountKey(acct)}) {
		t.Errorf("the deletion notified %q", got)
	}
}

// TestAccPreviewStoreUnavailable checks that every preview setting fails
// on a closed pool.
func TestAccPreviewStoreUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	acct, member := kernel.NewAccountID(), kernel.NewMemberID()
	for name, err := range map[string]error{
		"read": func() error {
			_, err := s.FeaturePreviews(ctx, acct)
			return err
		}(),
		"set": s.SetFeaturePreviews(ctx, acct, true),
		"personal": func() error {
			_, err := s.PersonalFeaturePreviews(ctx, acct, member)
			return err
		}(),
		"opt out":    s.SetPersonalFeaturePreviews(ctx, acct, member, false),
		"opt in":     s.SetPersonalFeaturePreviews(ctx, acct, member, true),
		"remove":     s.RemoveMember(ctx, acct, member),
		"delete all": s.DeleteAccount(ctx, acct),
	} {
		if err == nil {
			t.Errorf("%s on a closed pool succeeded", name)
		}
	}
}

// TestAccPreviewWritesAborted runs each write that notifies in a
// transaction a failed statement aborted.
func TestAccPreviewWritesAborted(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	for name, write := range map[string]func(context.Context) error{
		"set": func(ctx context.Context) error {
			return s.SetFeaturePreviews(ctx, kernel.NewAccountID(), true)
		},
		"delete": func(ctx context.Context) error {
			return s.DeleteAccount(ctx, kernel.NewAccountID())
		},
	} {
		errAborted := errors.New("aborted")
		err := tx.Do(ctx, db, tx.Options{}, func(ctx context.Context) error {
			_, _ = tx.Q(ctx, db).Exec(ctx, `SELECT 1/0`)
			if err := write(ctx); err == nil {
				t.Errorf("%s in an aborted transaction succeeded", name)
			}
			return errAborted
		})
		if !errors.Is(err, errAborted) {
			t.Fatal(err)
		}
	}
}
