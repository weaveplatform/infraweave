package pg_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// TestAccPolicyContextSlugs shares environment ordering and reads metadata even
// when opening an attached variable or mounted file would fail authentication.
func TestAccPolicyContextSlugs(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for _, in := range []core.Input{
		{Name: "Manual Zulu", Space: "team", Stacks: []core.StackInput{{Stack: "web", Priority: 1}}},
		{Name: "Manual Alpha", Space: "team", Stacks: []core.StackInput{{Stack: "web", Priority: 1}}},
		{Name: "Manual Late", Space: "team", Stacks: []core.StackInput{{Stack: "web", Priority: 5}}},
		{Name: "Auto Alpha", Space: "team", Labels: []string{"autoattach:*"}},
		{Name: "Auto Zulu", Space: "team", Labels: []string{"autoattach:*"}},
	} {
		in.Config = []core.ElementInput{
			{Name: "TOKEN", Value: new("private-context-token"), WriteOnly: true},
			{Name: "config.txt", Value: new("cHJpdmF0ZQ=="), FileMount: true},
		}
		in.Hooks = map[string][]string{"before_plan": {in.Name}}
		if _, err := f.svc.Create(ctx, f.admin, in); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"manual-alpha", "manual-zulu", "manual-late", "auto-zulu", "auto-alpha"}
	read := func() {
		t.Helper()
		got, err := f.svc.PolicyContextSlugs(ctx, f.acct, f.web.ID)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("effective contexts=%v, want %v, error=%v", got, want, err)
		}
	}
	read()
	resolved, err := f.svc.ResolveWithHooks(ctx, f.acct, f.web.ID, nil)
	if err != nil || !slices.Equal(resolved.Hooks["before_plan"], []string{"Manual Alpha", "Manual Zulu", "Manual Late", "Auto Zulu", "Auto Alpha"}) {
		t.Fatalf("environment order changed: hooks=%v error=%v", resolved.Hooks, err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE config_elements SET value_sealed='corrupt-value'::bytea WHERE account_id=$1`, f.acct); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Resolve(ctx, f.acct, f.web.ID); err == nil {
		t.Fatal("corrupt sealed values unexpectedly opened")
	}
	read()
	for _, scope := range []struct {
		account kernel.AccountID
		stack   kernel.ULID
	}{{kernel.NewAccountID(), f.web.ID}, {f.acct, f.data.ID}} {
		got, err := f.svc.PolicyContextSlugs(ctx, scope.account, scope.stack)
		if err != nil || len(got) != 0 {
			t.Fatalf("unattached/foreign contexts=%v error=%v", got, err)
		}
	}
	if _, err := f.svc.PolicyContextSlugs(ctx, f.acct, kernel.NewULID()); !errors.Is(err, configapi.ErrNoStack) {
		t.Fatalf("missing stack error=%v", err)
	}
	abort := errors.New("rollback attachment metadata")
	err = tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, f.db).Exec(ctx, `DELETE FROM context_attachments WHERE account_id=$1 AND stack_id=$2`, f.acct, f.web.ID); err != nil {
			return err
		}
		got, err := f.svc.PolicyContextSlugs(ctx, f.acct, f.web.ID)
		if err != nil {
			return err
		}
		if len(got) != 0 {
			return fmt.Errorf("context reader did not join its caller's transaction: %v", got)
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	read()
	err = tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
		var pid int32
		if err := tx.Q(ctx, f.db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		if _, err := f.db.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			return err
		}
		_, err := f.svc.PolicyContextSlugs(ctx, f.acct, f.web.ID)
		return err
	})
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("terminated context backend error=%v", err)
	}
	f.stacks.err = kernel.ErrUnavailable
	if _, err := f.svc.PolicyContextSlugs(ctx, f.acct, f.web.ID); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("stack owner backend error=%v", err)
	}
}
