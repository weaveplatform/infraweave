package pg_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccStackPolicyRolesAreCurrentDistinctAndAccountQualified(t *testing.T) {
	db := pool(t)
	x, foreign := seed(t, db, "policyroles"), seed(t, db, "foreignroles")
	stack, other := kernel.NewULID(), kernel.NewULID()
	custom := role.Role{AccountID: x.acct, ID: kernel.NewULID(), Slug: "deployer", Name: "Deployment", Version: 1}
	if err := x.s.CreateRole(t.Context(), custom); err != nil {
		t.Fatal(err)
	}
	for _, b := range []binding.Binding{
		bindingOf(t, x, binding.ActorStack, stack, role.SpaceReader, "policyroles", binding.OriginBinding),
		bindingOf(t, x, binding.ActorStack, stack, role.SpaceReader, "legacy", binding.OriginBinding),
		bindingOf(t, x, binding.ActorStack, stack, "deployer", "policyroles", binding.OriginBinding),
		bindingOf(t, x, binding.ActorUser, stack, role.SpaceAdmin, "policyroles", binding.OriginBinding),
		bindingOf(t, x, binding.ActorStack, other, role.SpaceWriter, "policyroles", binding.OriginBinding),
		bindingOf(t, foreign, binding.ActorStack, stack, role.SpaceWriter, "foreignroles", binding.OriginBinding),
	} {
		owner := x.s
		if b.AccountID == foreign.acct {
			owner = foreign.s
		}
		if _, err := owner.CreateBindings(t.Context(), b.AccountID, []binding.Binding{b}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) []accessapi.PolicyRole {
		t.Helper()
		got, err := x.s.StackRoles(t.Context(), x.acct, stack)
		if err != nil || len(got) != 2 {
			t.Fatalf("stack roles=%+v error=%v", got, err)
		}
		seenCustom := false
		for _, r := range got {
			switch r.Slug {
			case "deployer":
				seenCustom = true
				if r.ID != custom.ID || r.Name != name {
					t.Errorf("custom role metadata=%+v", r)
				}
			case role.SpaceReader:
				if r.ID.IsZero() || r.Name != "Space reader" {
					t.Errorf("system role metadata=%+v", r)
				}
			default:
				t.Errorf("role of another actor/account leaked: %+v", r)
			}
		}
		if !seenCustom || bytes.Compare(got[0].ID[:], got[1].ID[:]) >= 0 {
			t.Fatal("missing role or unstable immutable-ID order")
		}
		return got
	}
	read("Deployment")
	abort := errors.New("rollback role description")
	err := tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, db).Exec(ctx, "UPDATE roles SET name='Uncommitted' WHERE account_id=$1 AND id=$2", x.acct, custom.ID); err != nil {
			return err
		}
		got, err := x.s.StackRoles(ctx, x.acct, stack)
		if err != nil {
			return err
		}
		for _, r := range got {
			if r.ID == custom.ID && r.Name == "Uncommitted" {
				return abort
			}
		}
		return errors.New("role reader missed its caller's description update")
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	read("Deployment")
	if rows, err := x.s.StackRoles(t.Context(), kernel.NewAccountID(), stack); err != nil || len(rows) != 0 {
		t.Fatalf("foreign account roles=%+v error=%v", rows, err)
	}
	if rows, err := x.s.StackRoles(t.Context(), x.acct, kernel.NewULID()); err != nil || len(rows) != 0 {
		t.Fatalf("missing stack roles=%+v error=%v", rows, err)
	}
	err = tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		var pid int32
		if err := tx.Q(ctx, db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			return err
		}
		_, err := x.s.StackRoles(ctx, x.acct, stack)
		return err
	})
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("terminated role backend error=%v", err)
	}
}
