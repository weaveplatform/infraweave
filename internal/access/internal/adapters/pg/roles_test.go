package pg_test

import (
	"errors"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
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
		if g != w {
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

// TestUnitRoleBackfill checks that access 00010's backfill gives existing
// accounts the roles role.Seed gives new ones: the same slugs, names and
// descriptions.
func TestUnitRoleBackfill(t *testing.T) {
	data, err := fs.ReadFile(pg.Stream().Files, "00010_roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := regexp.MustCompile(`\s+`).ReplaceAllString(string(data), " ")
	for _, r := range role.Seed(kernel.NewAccountID()) {
		if row := "('" + r.Slug + "', '" + r.Name + "', '" + r.Description + "')"; !strings.Contains(
			sql,
			row,
		) {
			t.Errorf("the backfill lacks %s", row)
		}
	}
}
