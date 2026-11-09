package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// authzVersion reads acct's authz_version.
func authzVersion(t *testing.T, x seeded, acct kernel.AccountID) int64 {
	t.Helper()
	var v int64
	if err := x.db.QueryRow(t.Context(), `SELECT authz_version FROM accounts WHERE id = $1`,
		acct).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestAccAuthzData checks that the loader reads an account's tree, roles,
// custom actions, bindings and groups at its version, and that the
// snapshot built from them decides as the rows say.
func TestAccAuthzData(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "hooli")
	svc := treeOf(x.s)
	prod, err := svc.Create(ctx, x.acct, "hooli", space.Space{Name: "Prod", InheritEntities: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.CreateGroup(ctx, group.Group{
		AccountID: x.acct, ID: kernel.NewULID(), Name: "ops", CreatedAt: spacesAt,
	}, onTrail(x, group.ActionCreate)); err != nil {
		t.Fatal(err)
	}
	custom := kernel.NewULID()
	if _, err := x.db.Exec(ctx, `INSERT INTO roles (account_id, id, slug, name)
		VALUES ($1, $2, 'cycler', 'Cycler')`, x.acct, custom); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO role_actions (account_id, role_id, action)
		VALUES ($1, $2, 'WORKER_POOL_CYCLE'), ($1, $2, 'SPACE_READ')`, x.acct, custom); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO role_bindings (account_id, id, actor_kind, actor_id,
			role_id, space_id, origin, created_by)
		VALUES ($1, gen_random_uuid(), 'group', $2, $3, $4, 'binding', 'test')`,
		x.acct, kernel.NewULID(), custom, prod.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO role_bindings (account_id, id, actor_kind, actor_id,
			role_id, space_id, origin, created_by)
		SELECT $1, gen_random_uuid(), 'group', g.id, $2, $3, 'binding', 'test'
		FROM managed_user_groups g WHERE g.account_id = $1 AND g.name = 'ops'`,
		x.acct, custom, prod.ID); err != nil {
		t.Fatal(err)
	}

	d, err := x.s.AuthzData(ctx, x.acct)
	if err != nil {
		t.Fatal(err)
	}
	if d.Account != x.acct || d.Version != authzVersion(t, x, x.acct) {
		t.Errorf("AuthzData is of %s at %d", d.Account, d.Version)
	}
	if len(d.Spaces) != 3 || len(d.Roles) != 6 || len(d.Bindings) != 2 || len(d.Groups) != 1 {
		t.Errorf("AuthzData has %d spaces, %d roles, %d bindings, %d groups",
			len(d.Spaces), len(d.Roles), len(d.Bindings), len(d.Groups))
	}
	for _, sp := range d.Spaces {
		if (sp.ID == x.acct.ULID) != sp.Parent.IsZero() || (sp.ID == prod.ID) != sp.Inherit {
			t.Errorf("space %+v", sp)
		}
	}
	for _, r := range d.Roles {
		want := []accessapi.Action(nil)
		if r.ID == custom {
			want = []accessapi.Action{accessapi.ActionSpaceRead, accessapi.ActionWorkerPoolCycle}
		}
		if r.System == (r.ID == custom) || !slices.Equal(r.Actions, want) {
			t.Errorf("role %+v", r)
		}
	}

	snap := authz.Build(d)
	ops := authz.Who{
		Account: x.acct, Kind: kernel.PrincipalUser, Actor: x.member.ULID,
		Member: x.member, Teams: []string{"ops"},
	}
	if !snap.Authorize(ops, accessapi.ActionWorkerPoolCycle, prod.ID) ||
		snap.Authorize(ops, accessapi.ActionWorkerPoolCycle, x.acct.ULID) {
		t.Error("the ops team's custom role decides otherwise than its binding")
	}
	// The ops binding on Prod, which inherits, reads its parent.
	if !snap.Authorize(ops, accessapi.ActionSpaceRead, x.acct.ULID) {
		t.Error("an inherited read is refused")
	}

	if _, err := x.s.AuthzData(ctx, kernel.NewAccountID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("AuthzData of no account = %v", err)
	}
	gone := kernel.NewAccountID()
	got, err := x.s.AuthzVersions(ctx, []kernel.AccountID{x.acct, gone})
	if err != nil || len(got) != 1 || got[x.acct] != d.Version {
		t.Errorf("AuthzVersions = %v, %v", got, err)
	}
}

// TestAccAuthzInvalidation checks that every write a snapshot reads moves
// the account's authz_version and sends authz:<account> on commit, and a
// refused write, or a rename, which no snapshot reads, does neither.
func TestAccAuthzInvalidation(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "endframe")
	c := listen(t, x.db)
	svc := treeOf(x.s)
	want := []string{authz.Key(x.acct)}
	var created space.Space
	var ops group.Group
	for _, w := range []struct {
		name  string
		write func() error
	}{
		{"create a space", func() (err error) {
			created, err = svc.Create(ctx, x.acct, "endframe", space.Space{Name: "Prod"})
			return err
		}},
		{"make a space inherit", func() error {
			in := created
			in.InheritEntities = true
			_, err := svc.svc.Update(ctx, admin(x.acct), created.Slug, "endframe", kernel.Expect{}, in)
			return err
		}},
		{"move a space", func() error {
			return svc.Move(ctx, x.acct, created.Slug, space.LegacySlug)
		}},
		{"delete a space", func() error { return svc.Delete(ctx, x.acct, created.Slug) }},
		{"create a group", func() (err error) {
			ops, err = x.s.CreateGroup(ctx, group.Group{
				AccountID: x.acct, ID: kernel.NewULID(), Name: "ops", CreatedAt: spacesAt,
			}, onTrail(x, group.ActionCreate))
			return err
		}},
		{"delete a group", func() error {
			return x.s.DeleteGroup(ctx, x.acct, ops.ID, kernel.Expect{}, onTrail(x, group.ActionDelete))
		}},
		{"claim the account", func() error {
			_, err := x.s.ClaimAccount(ctx, x.org, x.acct, x.member, spacesAt,
				byAdmin(x, orgadmin.ActionAccountClaim))
			return err
		}},
	} {
		before := authzVersion(t, x, x.acct)
		if err := w.write(); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		if got := messages(t, c); !slices.Equal(got, want) {
			t.Errorf("%s sent %v, want %v", w.name, got, want)
		}
		if v := authzVersion(t, x, x.acct); v <= before {
			t.Errorf("%s left authz_version at %d", w.name, v)
		}
	}

	before := authzVersion(t, x, x.acct)
	if _, err := svc.Create(ctx, x.acct, "nope", space.Space{Name: "x"}); err == nil {
		t.Fatal("a create below a missing space succeeded")
	}
	if err := svc.Delete(ctx, x.acct, "endframe"); err == nil {
		t.Fatal("deleting the top space succeeded")
	}
	if _, err := svc.svc.Update(ctx, admin(x.acct), space.LegacySlug, "endframe",
		kernel.Expect{},
		space.Space{Name: "Old", Labels: []string{"kept"}}); err != nil {
		t.Fatalf("rename legacy: %v", err)
	}
	if got := messages(t, c); len(got) != 0 {
		t.Errorf("refused writes sent %v", got)
	}
	if v := authzVersion(t, x, x.acct); v != before {
		t.Errorf("refused writes moved authz_version to %d from %d", v, before)
	}
	// A seeded account's snapshot holds its tree and roles from the start.
	if d, err := x.s.AuthzData(ctx, x.acct); err != nil || len(d.Roles) != len(role.Seed(x.acct)) {
		t.Errorf("AuthzData = %d roles, %v", len(d.Roles), err)
	}
}

// TestAccAuthzUnavailable checks that both reads fail, and not as not
// found, when PostgreSQL cannot be reached.
func TestAccAuthzUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	acct := kernel.NewAccountID()
	for name, err := range map[string]error{
		"data":     func() error { _, err := s.AuthzData(ctx, acct); return err }(),
		"versions": func() error { _, err := s.AuthzVersions(ctx, []kernel.AccountID{acct}); return err }(),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}
