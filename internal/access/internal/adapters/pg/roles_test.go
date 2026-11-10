package pg_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// seededRoles reads acct's roles as role.Seed makes them, by slug.
func seededRoles(t *testing.T, x seeded, acct kernel.AccountID) map[string]role.Role {
	t.Helper()
	rows, err := x.db.Query(t.Context(), `SELECT account_id, id, slug, name, description, is_system
		FROM roles WHERE account_id = $1`, acct)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (role.Role, error) {
		var x role.Role
		return x, r.Scan(&x.AccountID, &x.ID, &x.Slug, &x.Name, &x.Description, &x.System)
	})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]role.Role{}
	for _, r := range got {
		out[r.Slug] = r
	}
	return out
}

// sameRoles reports how got differs from the roles role.Seed gives acct,
// ignoring their IDs.
func sameRoles(acct kernel.AccountID, got map[string]role.Role) []string {
	var problems []string
	want := role.Seed(acct)
	if len(got) != len(want) {
		problems = append(problems, "a role count other than the system roles'")
	}
	for _, w := range want {
		g := got[w.Slug]
		g.ID = w.ID
		if !reflect.DeepEqual(g, w) {
			problems = append(problems, w.Slug)
		}
	}
	return problems
}

// adminBindings are acct's space-admin bindings on its top space, as
// "<kind>:<actor> by <created_by>".
func adminBindings(t *testing.T, x seeded, acct kernel.AccountID) []string {
	t.Helper()
	rows, err := x.db.Query(t.Context(), `SELECT b.actor_kind || ':' || b.actor_id || ' by ' ||
			b.created_by || ' as ' || b.origin
		FROM role_bindings b JOIN roles r ON r.id = b.role_id
		WHERE b.account_id = $1 AND b.space_id = $1 AND r.slug = 'space-admin'`, acct)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAccRoleSeeding checks that both ways of creating an account give it
// the five system roles, and creating one binds space-admin on its top
// space to the account admin it names.
func TestAccRoleSeeding(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "raviga")
	if p := sameRoles(x.acct, seededRoles(t, x, x.acct)); len(p) != 0 {
		t.Errorf("a new organization's account's roles differ: %v", p)
	}
	if b := adminBindings(t, x, x.acct); len(b) != 0 {
		t.Errorf("an unclaimed account binds %v", b)
	}
	b, err := account.New(x.org, "", "raviga-two", "Raviga Two")
	if err != nil {
		t.Fatal(err)
	}
	e := byAdmin(x, orgadmin.ActionAccountCreate)
	if err := x.s.CreateAccount(ctx, b, x.member, e); err != nil {
		t.Fatal(err)
	}
	if p := sameRoles(b.ID, seededRoles(t, x, b.ID)); len(p) != 0 {
		t.Errorf("a created account's roles differ: %v", p)
	}
	want := []string{"user:" + x.member.UUID() + " by " + string(e.ActorKind) + ":" + e.ActorID +
		" as binding"}
	if got := adminBindings(t, x, b.ID); !slices.Equal(got, want) {
		t.Errorf("a created account's admin bindings = %v, want %v", got, want)
	}
}

// TestAccClaimBinding checks that a claim binds space-admin on the top
// space to the member it names in its transaction: a refused claim and a
// second claim write none.
func TestAccClaimBinding(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "bream-hall")
	claim := byAdmin(x, orgadmin.ActionAccountClaim)
	bob := addMember(t, x, "bob", "deactivated")
	if _, err := x.s.ClaimAccount(ctx, x.org, x.acct, bob, spacesAt,
		claim); !errors.Is(err, orgadmin.ErrNoAdmin) {
		t.Fatalf("claim for a deactivated member = %v", err)
	}
	if b := adminBindings(t, x, x.acct); len(b) != 0 {
		t.Errorf("a refused claim bound %v", b)
	}
	if _, err := x.s.ClaimAccount(ctx, x.org, x.acct, x.member, spacesAt, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.ClaimAccount(ctx, x.org, x.acct, x.member, spacesAt,
		claim); !errors.Is(err, orgadmin.ErrInvalidState) {
		t.Fatalf("a second claim = %v", err)
	}
	want := []string{"user:" + x.member.UUID() + " by " + string(claim.ActorKind) + ":" +
		claim.ActorID + " as binding"}
	if got := adminBindings(t, x, x.acct); !slices.Equal(got, want) {
		t.Errorf("the claimed account's admin bindings = %v, want %v", got, want)
	}
}

// TestAccAPIKeyAdmin checks that a key reads admin exactly while it holds
// space-admin on its account's top space: not a binding below the top
// space, nor another role there.
func TestAccAPIKeyAdmin(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "laurie")
	other := kernel.NewULID()
	if _, err := x.db.Exec(ctx, `INSERT INTO api_keys (account_id, id, name, type, secret_hash,
		teams, creator) VALUES ($1, $2, 'other', 'SECRET', sha256('other'), '{}', 'test')`,
		x.acct, other); err != nil {
		t.Fatal(err)
	}
	bind := func(key kernel.ULID, slug, space string) {
		t.Helper()
		if _, err := x.db.Exec(ctx, `INSERT INTO role_bindings (account_id, id, actor_kind,
				actor_id, role_id, space_id, origin, created_by)
			SELECT $1, gen_random_uuid(), 'api_key', $2, r.id, s.id, 'binding', 'test'
			FROM roles r, spaces s
			WHERE r.account_id = $1 AND r.slug = $3 AND s.account_id = $1 AND s.slug = $4`,
			x.acct, key, slug, space); err != nil {
			t.Fatal(err)
		}
	}
	bind(other, role.SpaceAdmin, "legacy")
	bind(other, role.SpaceWriter, "laurie")
	bind(x.key, role.SpaceAdmin, "laurie")
	for id, want := range map[kernel.ULID]bool{x.key: true, other: false} {
		k, err := x.s.AccountAPIKey(ctx, x.acct, id)
		if err != nil || k.Admin != want {
			t.Errorf("key %s admin = %t, %v; want %t", id, k.Admin, err, want)
		}
	}
	keys, err := x.s.AccountAPIKeys(ctx, x.acct, kernel.ULID{}, 10)
	if err != nil || len(keys) != 2 {
		t.Fatalf("AccountAPIKeys = %v, %v", keys, err)
	}
	for _, k := range keys {
		if k.Admin != (k.ID == x.key) {
			t.Errorf("listed key %s admin = %t", k.ID, k.Admin)
		}
	}
}

// TestAccRoleDeletion checks that deleting an account, or its
// organization, deletes its roles, their actions and its bindings.
func TestAccRoleDeletion(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "gilfoyle")
	b, err := account.New(x.org, "", "gilfoyle-two", "Gilfoyle Two")
	if err != nil {
		t.Fatal(err)
	}
	if err := x.s.CreateAccount(
		ctx,
		b,
		x.member,
		byAdmin(x, orgadmin.ActionAccountCreate),
	); err != nil {
		t.Fatal(err)
	}
	for _, acct := range []kernel.AccountID{x.acct, b.ID} {
		if _, err := x.db.Exec(ctx, `INSERT INTO roles (account_id, id, slug, name)
			VALUES ($1, gen_random_uuid(), 'custom', 'Custom')`, acct); err != nil {
			t.Fatal(err)
		}
		if _, err := x.db.Exec(ctx, `INSERT INTO role_actions (account_id, role_id, action)
			SELECT account_id, id, 'SPACE_READ' FROM roles WHERE account_id = $1 AND slug = 'custom'`,
			acct); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := x.s.ClaimAccount(ctx, x.org, x.acct, x.member, spacesAt,
		byAdmin(x, orgadmin.ActionAccountClaim)); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.RemoveAccount(ctx, x.org, b.ID,
		byAdmin(x, orgadmin.ActionAccountDelete)); err != nil {
		t.Fatal(err)
	}
	tables := []string{"roles", "role_actions", "role_bindings"}
	for _, table := range tables {
		if n := count(t, x, table, b.ID); n != 0 {
			t.Errorf("%s rows of a deleted account = %d", table, n)
		}
		if n := count(t, x, table, x.acct); n == 0 {
			t.Errorf("deleting an account deleted its sibling's %s", table)
		}
	}
	if _, err := x.s.DeleteOrganization(ctx, x.org, byAdmin(x, "organization.delete")); err != nil {
		t.Fatalf("delete an organization whose account has roles and bindings: %v", err)
	}
	for _, table := range tables {
		if n := count(t, x, table, x.acct); n != 0 {
			t.Errorf("%s rows of a deleted organization's account = %d", table, n)
		}
	}
}

// TestAccCustomRoles checks the store under the role service: a custom
// role is stored with its actions and lists beside the system roles, an
// update replaces its actions and raises its version, a taken slug is
// refused, and a role with a binding is not deleted. Each write raises
// the account's authz_version.
func TestAccCustomRoles(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "hooli")
	svc := role.NewService(role.Deps{Store: x.s})
	authz := func() int64 {
		t.Helper()
		var v int64
		if err := x.db.QueryRow(ctx, `SELECT authz_version FROM accounts WHERE id = $1`,
			x.acct).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := authz()
	r, err := svc.Create(ctx, x.acct, role.Input{
		Name:    "Deploy Bots",
		Actions: []accessapi.Action{"STACK_MANAGE", "SPACE_READ"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, x.acct, role.Input{
		Name:    "deploy-bots",
		Actions: []accessapi.Action{},
	}); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("a taken slug = %v", err)
	}
	got, err := svc.Role(ctx, x.acct, "deploy-bots")
	if err != nil || got.ID != r.ID || got.Version != 1 || got.Bindings != 0 ||
		!slices.Equal(got.Actions, []accessapi.Action{"SPACE_READ", "STACK_MANAGE"}) {
		t.Errorf("Role = %+v, %v", got, err)
	}
	all, err := svc.Roles(ctx, x.acct, "", 10)
	if err != nil || len(all) != 6 {
		t.Fatalf("Roles = %v, %v", all, err)
	}
	page, err := svc.Roles(ctx, x.acct, "deploy-bots", 2)
	if err != nil || len(page) != 2 || page[0].Slug != role.DriftDetectionManager ||
		page[1].Slug != role.SpaceAdmin || len(page[1].Actions) == 0 {
		t.Errorf("a page after deploy-bots = %+v, %v", page, err)
	}

	actions := []accessapi.Action{"SPACE_WRITE"}
	up, err := svc.Update(ctx, x.acct, "deploy-bots", kernel.ExpectVersions(1),
		role.Change{Name: new("Deployers"), Actions: &actions})
	if err != nil || up.Slug != "deploy-bots" || up.Version != 2 {
		t.Fatalf("Update = %+v, %v", up, err)
	}
	got, err = svc.Role(ctx, x.acct, "deploy-bots")
	if err != nil || got.Name != "Deployers" || !slices.Equal(got.Actions, actions) {
		t.Errorf("after the update, Role = %+v, %v", got, err)
	}
	if _, err := svc.Update(ctx, x.acct, "deploy-bots", kernel.ExpectVersions(1),
		role.Change{}); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale update = %v", err)
	}
	if _, err := svc.Update(ctx, x.acct, "nobody", kernel.Expect{},
		role.Change{}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a missing role's update = %v", err)
	}

	if _, err := x.db.Exec(ctx, `INSERT INTO role_bindings (account_id, id, actor_kind,
			actor_id, role_id, space_id, origin, created_by)
		VALUES ($1, gen_random_uuid(), 'user', $2, $3, $1, 'binding', 'test')`,
		x.acct, x.member, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Delete(ctx, x.acct, "deploy-bots",
		kernel.Expect{}); !errors.Is(err, role.ErrInUse) {
		t.Errorf("a bound role's delete = %v", err)
	}
	b, err := svc.Bindings(ctx, x.acct, r)
	if err != nil || len(b) != 1 || b[0].Space != "hooli" || b[0].ActorKind != binding.ActorUser ||
		b[0].ActorID != x.member.ULID {
		t.Errorf("Bindings = %+v, %v", b, err)
	}
	if _, err := x.db.Exec(ctx, `DELETE FROM role_bindings WHERE role_id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Delete(ctx, x.acct, "deploy-bots", kernel.ExpectVersions(2)); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := x.db.QueryRow(ctx, `SELECT count(*) FROM role_actions WHERE role_id = $1`,
		r.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("%d actions remain, %v", left, err)
	}
	if _, err := svc.Role(ctx, x.acct, "deploy-bots"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a deleted role = %v", err)
	}
	// The create, the update and the delete each raise it once.
	if after := authz(); after != before+3 {
		t.Errorf("authz_version %d, then %d; want 3 more", before, after)
	}
}

// TestAccRoleStoreUnavailable checks that a change of a role failing
// within its transaction changes nothing, and that every read and change
// fails, and not as not found, when PostgreSQL cannot be reached.
func TestAccRoleStoreUnavailable(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "aviato")
	r := role.Role{
		AccountID: x.acct, ID: kernel.NewULID(), Slug: "ops", Name: "Ops", Version: 1,
		Actions: []accessapi.Action{"SPACE_READ"},
	}
	if err := x.s.CreateRole(ctx, r); err != nil {
		t.Fatal(err)
	}
	// Triggers of this test's own database fail each write within its
	// transaction: an action named REFUSED, a role renamed refused, and a
	// role's delete.
	if _, err := x.db.Exec(ctx, `
		CREATE FUNCTION refuse() RETURNS trigger LANGUAGE plpgsql AS
			$$BEGIN RAISE EXCEPTION 'refused'; END$$;
		CREATE TRIGGER refuse_action BEFORE INSERT ON role_actions FOR EACH ROW
			WHEN (NEW.action = 'REFUSED') EXECUTE FUNCTION refuse();
		CREATE TRIGGER refuse_rename BEFORE UPDATE ON roles FOR EACH ROW
			WHEN (NEW.name = 'refused') EXECUTE FUNCTION refuse();
		CREATE TRIGGER refuse_delete BEFORE DELETE ON roles FOR EACH ROW
			EXECUTE FUNCTION refuse();`); err != nil {
		t.Fatal(err)
	}
	refused := r
	refused.ID, refused.Slug, refused.Actions = kernel.NewULID(), "refused", []accessapi.Action{
		"REFUSED",
	}
	edit := func(name string, actions ...accessapi.Action) func(role.Role) (role.Role, error) {
		return func(r role.Role) (role.Role, error) {
			r.Name, r.Actions = name, actions
			return r, nil
		}
	}
	for name, err := range map[string]error{
		"create": x.s.CreateRole(ctx, refused),
		"rename": func() error { _, err := x.s.UpdateRole(ctx, x.acct, "ops", edit("refused")); return err }(),
		"actions": func() error {
			_, err := x.s.UpdateRole(ctx, x.acct, "ops", edit("Ops", "REFUSED"))
			return err
		}(),
		"delete": func() error {
			_, err := x.s.DeleteRole(ctx, x.acct, "ops", func(role.Role) error { return nil })
			return err
		}(),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s failing in its transaction = %v", name, err)
		}
	}
	if got, err := x.s.Role(
		ctx,
		x.acct,
		"ops",
	); err != nil || got.Name != "Ops" ||
		got.Version != 1 ||
		!slices.Equal(got.Actions, r.Actions) {
		t.Errorf("after the failed writes, ops = %+v, %v", got, err)
	}

	p := pool(t)
	s := pg.New(p)
	p.Close()
	bg := context.Background()
	keep := func(r role.Role) (role.Role, error) { return r, nil }
	for name, err := range map[string]error{
		"list":     func() error { _, err := s.Roles(bg, x.acct, "", 10); return err }(),
		"read":     func() error { _, err := s.Role(bg, x.acct, "ops"); return err }(),
		"create":   s.CreateRole(bg, r),
		"update":   func() error { _, err := s.UpdateRole(bg, x.acct, "ops", keep); return err }(),
		"delete":   func() error { _, err := s.DeleteRole(bg, x.acct, "ops", nil); return err }(),
		"bindings": func() error { _, err := s.RoleBindings(bg, x.acct, r.ID); return err }(),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s with PostgreSQL closed = %v", name, err)
		}
	}
}
