package pg_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// spacesAt is when the spaces these tests create are created.
var spacesAt = time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

// everything is a space.Authorizer allowing every action, so a test drives
// a tree as its account's admin.
type everything struct{}

func (everything) Authorize(
	context.Context,
	kernel.Principal,
	accessapi.Action,
	kernel.ULID,
) (bool, error) {
	return true, nil
}

// adminTree drives each account's tree through the space service as the
// account's admin.
type adminTree struct{ svc *space.Service }

func treeOf(s space.Store) adminTree {
	return adminTree{space.NewService(s, everything{}, func() time.Time { return spacesAt })}
}

func admin(acct kernel.AccountID) kernel.Principal {
	return kernel.Principal{Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: acct}
}

func (a adminTree) Create(
	ctx context.Context,
	acct kernel.AccountID,
	parent string,
	in space.Space,
) (space.Space, error) {
	return a.svc.Create(ctx, admin(acct), parent, in)
}

// Move moves acct's space slug below parent, keeping its other fields.
func (a adminTree) Move(ctx context.Context, acct kernel.AccountID, slug, parent string) error {
	cur, err := a.svc.Space(ctx, acct, slug)
	if err != nil {
		return err
	}
	_, err = a.svc.Update(ctx, admin(acct), slug, parent, cur)
	return err
}

func (a adminTree) Delete(ctx context.Context, acct kernel.AccountID, slug string) error {
	_, err := a.svc.Delete(ctx, admin(acct), slug)
	return err
}

// closureRow is one row of space_closure.
type closureRow struct {
	ancestor, descendant kernel.ULID
	depth                int
}

// closureProblems compares acct's space_closure with the ancestors its
// spaces' parent_id links give, and names every difference.
func closureProblems(t *testing.T, x seeded, acct kernel.AccountID) []string {
	t.Helper()
	ctx := t.Context()
	rows, err := x.db.Query(ctx, `SELECT id, coalesce(parent_id, id) FROM spaces
		WHERE account_id = $1`, acct)
	if err != nil {
		t.Fatal(err)
	}
	parent := map[kernel.ULID]kernel.ULID{}
	var id, p kernel.ULID
	if _, err := pgx.ForEachRow(rows, []any{&id, &p}, func() error {
		parent[id] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[closureRow]bool{}
	for d := range parent {
		for a, depth := d, 0; ; a, depth = parent[a], depth+1 {
			want[closureRow{a, d, depth}] = true
			if parent[a] == a || depth > len(parent) {
				break
			}
		}
	}
	rows, err = x.db.Query(ctx, `SELECT ancestor_id, descendant_id, depth FROM space_closure
		WHERE account_id = $1`, acct)
	if err != nil {
		t.Fatal(err)
	}
	got := map[closureRow]bool{}
	var r closureRow
	if _, err := pgx.ForEachRow(rows, []any{&r.ancestor, &r.descendant, &r.depth}, func() error {
		got[r] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var out []string
	for w := range want {
		if !got[w] {
			out = append(out, fmt.Sprintf("missing %v", w))
		}
	}
	for g := range got {
		if !want[g] {
			out = append(out, fmt.Sprintf("extra %v", g))
		}
	}
	return out
}

// count is the number of rows of table naming acct.
func count(t *testing.T, x seeded, table string, acct kernel.AccountID) int {
	t.Helper()
	var n int
	if err := x.db.QueryRow(t.Context(), `SELECT count(*) FROM `+table+
		` WHERE account_id = $1`, acct).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAccSpaceSeeding checks that both ways of creating an account give it
// its top space, named by the account's slug, and legacy beneath it, and
// that a refused creation leaves no tree.
func TestAccSpaceSeeding(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "hooli")
	got, err := x.s.Spaces(ctx, x.acct)
	if err != nil || len(got) != 2 {
		t.Fatalf("Spaces = %+v, %v", got, err)
	}
	if top := got[0]; !top.Top() || top.ID != x.acct.ULID || top.Slug != "hooli" ||
		top.Name != "Account hooli" || top.CreatedAt.IsZero() {
		t.Errorf("top = %+v", top)
	}
	if legacy := got[1]; legacy.Slug != space.LegacySlug || legacy.ParentID != x.acct.ULID {
		t.Errorf("legacy = %+v", legacy)
	}
	if n := count(t, x, "space_closure", x.acct); n != 3 {
		t.Errorf("closure rows = %d, want 3", n)
	}
	if p := closureProblems(t, x, x.acct); len(p) != 0 {
		t.Errorf("closure: %v", p)
	}

	b, err := account.New(x.org, "", "hooli-two", "Hooli Two")
	if err != nil {
		t.Fatal(err)
	}
	b.CreatedAt, b.ClaimedAt = spacesAt, spacesAt
	if err := x.s.CreateAccount(
		ctx,
		b,
		x.member,
		byAdmin(x, orgadmin.ActionAccountCreate),
	); err != nil {
		t.Fatal(err)
	}
	got, err = x.s.Spaces(ctx, b.ID)
	if err != nil || len(got) != 2 || got[0].Slug != "hooli-two" ||
		!got[0].CreatedAt.Equal(spacesAt) {
		t.Errorf("created account's spaces = %+v, %v", got, err)
	}
	dup := b
	dup.ID = kernel.NewAccountID()
	if err := x.s.CreateAccount(ctx, dup, x.member,
		byAdmin(x, orgadmin.ActionAccountCreate)); !errors.Is(err, port.ErrSlugTaken) {
		t.Fatalf("a slug in use = %v", err)
	}
	if n := count(t, x, "spaces", dup.ID); n != 0 {
		t.Errorf("a refused creation left %d spaces", n)
	}
	if _, err := x.s.Spaces(ctx, kernel.NewAccountID()); err != nil {
		t.Errorf("spaces of no account = %v", err)
	}
}

// TestAccSpaceTree drives creates, moves and deletes through the service
// on the store, then checks the refusals.
func TestAccSpaceTree(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "aviato")
	svc := treeOf(x.s)
	prod, err := svc.Create(ctx, x.acct, "aviato", space.Space{
		Name:            "Production",
		Description:     "live",
		Labels:          []string{"env:prod"},
		InheritEntities: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	eu, err := svc.Create(ctx, x.acct, prod.Slug, space.Space{Name: "EU"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := x.s.Space(ctx, x.acct, prod.Slug)
	if err != nil || got.Description != "live" || !slices.Equal(got.Labels, []string{"env:prod"}) ||
		!got.InheritEntities || !got.CreatedAt.Equal(spacesAt) {
		t.Errorf("Space = %+v, %v", got, err)
	}
	if _, err := svc.Create(
		ctx,
		x.acct,
		"nope",
		space.Space{Name: "x"},
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("create below a missing space = %v", err)
	}
	other := seed(t, x.db, "nucleus")
	if err := x.s.CreateSpace(ctx, space.Space{
		AccountID: x.acct, ID: kernel.NewULID(), Slug: "x", Name: "x", ParentID: other.acct.ULID,
	}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("create below another account's space = %v", err)
	}
	if err := x.s.CreateSpace(ctx, space.Space{
		AccountID: kernel.NewAccountID(), ID: kernel.NewULID(), Slug: "x", Name: "x",
	}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("create in an account with no tree = %v", err)
	}

	if err := svc.Move(ctx, x.acct, prod.Slug, eu.Slug); err == nil {
		t.Error("a move below its own child succeeded")
	} else if _, ok := errors.AsType[*space.Refusal](err); !ok {
		t.Errorf("a move below its own child = %v, want a refusal", err)
	}
	if err := svc.Move(ctx, x.acct, prod.Slug, prod.Slug); err == nil {
		t.Error("a move below itself succeeded")
	}
	if err := svc.Move(ctx, x.acct, eu.Slug, space.LegacySlug); err != nil {
		t.Errorf("Move = %v", err)
	}
	if err := svc.Move(ctx, x.acct, eu.Slug, space.LegacySlug); err != nil {
		t.Errorf("a move to the same parent = %v", err)
	}
	if err := svc.Move(ctx, x.acct, "nope", "aviato"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("move a missing space = %v", err)
	}
	if err := svc.Move(ctx, x.acct, eu.Slug, "nope"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("move below a missing space = %v", err)
	}
	if err := svc.Move(ctx, x.acct, prod.Slug, eu.Slug); err != nil {
		t.Errorf("move below a former child = %v", err)
	}
	if p := closureProblems(t, x, x.acct); len(p) != 0 {
		t.Errorf("closure after moves: %v", p)
	}

	if err := svc.Delete(ctx, x.acct, eu.Slug); err == nil {
		t.Error("deleting a space with a child succeeded")
	} else if r, ok := errors.AsType[*space.Refusal](err); !ok || len(r.Reasons) != 1 {
		t.Errorf("deleting a space with a child = %v", err)
	}
	if err := svc.Delete(ctx, x.acct, "nope"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("delete a missing space = %v", err)
	}
	for _, slug := range []string{prod.Slug, eu.Slug} {
		if err := svc.Delete(ctx, x.acct, slug); err != nil {
			t.Errorf("Delete %s = %v", slug, err)
		}
	}
	if n := count(t, x, "spaces", x.acct); n != 2 {
		t.Errorf("spaces left = %d, want the seeded 2", n)
	}
	if p := closureProblems(t, x, x.acct); len(p) != 0 {
		t.Errorf("closure after deletes: %v", p)
	}
	if err := x.s.DeleteSpace(ctx, kernel.NewAccountID(), "x",
		nil); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("delete in an account with no tree = %v", err)
	}
}

// TestAccClosureProperty checks that, after random creates, moves and
// deletes, space_closure equals the ancestors that parent_id gives, and
// every refusal is one the tree's shape explains.
func TestAccClosureProperty(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "pied-piper")
	svc := treeOf(x.s)
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // a reproducible test sequence
	for i := range 200 {
		spaces, err := x.s.Spaces(ctx, x.acct)
		if err != nil {
			t.Fatal(err)
		}
		pick := func() space.Space { return spaces[r.IntN(len(spaces))] }
		switch op := r.IntN(3); {
		case op == 0 || len(spaces) < 4:
			if _, err := svc.Create(ctx, x.acct, pick().Slug,
				space.Space{Name: fmt.Sprintf("s%d", i)}); err != nil {
				t.Fatalf("step %d create: %v", i, err)
			}
		case op == 1:
			s, p := pick(), pick()
			err := svc.Move(ctx, x.acct, s.Slug, p.Slug)
			if _, refused := errors.AsType[*space.Refusal](err); err != nil && !refused {
				t.Fatalf("step %d move: %v", i, err)
			}
		default:
			s := pick()
			err := svc.Delete(ctx, x.acct, s.Slug)
			if _, refused := errors.AsType[*space.Refusal](err); err != nil && !refused {
				t.Fatalf("step %d delete: %v", i, err)
			}
		}
		if p := closureProblems(t, x, x.acct); len(p) != 0 {
			t.Fatalf("step %d: closure differs: %v", i, p)
		}
	}
}

// TestAccSpaceDeletion checks that deleting an account, or its
// organization, deletes its tree, including an account with settings.
func TestAccSpaceDeletion(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "initech")
	b, err := account.New(x.org, "", "initech-two", "Initech Two")
	if err != nil {
		t.Fatal(err)
	}
	b.CreatedAt, b.ClaimedAt = spacesAt, spacesAt
	if err := x.s.CreateAccount(
		ctx,
		b,
		x.member,
		byAdmin(x, orgadmin.ActionAccountCreate),
	); err != nil {
		t.Fatal(err)
	}
	svc := treeOf(x.s)
	if _, err := svc.Create(ctx, b.ID, "initech-two", space.Space{Name: "Dev"}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.RemoveAccount(ctx, x.org, b.ID,
		byAdmin(x, orgadmin.ActionAccountDelete)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"spaces", "space_closure"} {
		if n := count(t, x, table, b.ID); n != 0 {
			t.Errorf("%s rows of a deleted account = %d", table, n)
		}
	}

	if err := x.s.SetSecurityEmail(ctx, x.acct, "security@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, x.acct, "initech", space.Space{Name: "Prod"}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.DeleteOrganization(ctx, x.org, byAdmin(x, "organization.delete")); err != nil {
		t.Fatalf("delete an organization whose account has settings and spaces: %v", err)
	}
	for _, table := range []string{"spaces", "space_closure", "account_settings"} {
		if n := count(t, x, table, x.acct); n != 0 {
			t.Errorf("%s rows of a deleted organization's account = %d", table, n)
		}
	}
}

// TestAccSpaceStoreUnavailable checks that every read and change of a tree
// fails, and not as not found, when PostgreSQL cannot be reached or the
// transaction fails after a check.
func TestAccSpaceStoreUnavailable(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "endframe")
	svc := treeOf(x.s)
	child, err := svc.Create(ctx, x.acct, "endframe", space.Space{Name: "Child"})
	if err != nil {
		t.Fatal(err)
	}
	abort := func(ctx context.Context) {
		_, _ = tx.Q(ctx, x.db).Exec(ctx, `SELECT 1/0`)
	}
	for name, err := range map[string]error{
		"move": func() error {
			_, err := x.s.UpdateSpace(ctx, x.acct, child.Slug, space.LegacySlug,
				func(ctx context.Context, cur, _ space.Space, _ *accessapi.SpaceMove) (space.Space, error) {
					abort(ctx)
					return cur, nil
				})
			return err
		}(),
		"delete": x.s.DeleteSpace(ctx, x.acct, child.Slug, func(ctx context.Context, _ space.Space) error {
			abort(ctx)
			return nil
		}),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s in a transaction failed after its check = %v", name, err)
		}
	}

	p := pool(t)
	s := pg.New(p)
	p.Close()
	bg := context.Background()
	o, a := tenant(t, "closed-spaces", "closed-spaces")
	allow := func(context.Context, space.Space) error { return nil }
	keep := func(_ context.Context, cur, _ space.Space, _ *accessapi.SpaceMove) (space.Space, error) {
		return cur, nil
	}
	for name, err := range map[string]error{
		"organization": s.CreateOrganization(bg, o, a, nil, created(o)),
		"list":         func() error { _, err := s.Spaces(bg, x.acct); return err }(),
		"read":         func() error { _, err := s.Space(bg, x.acct, "x"); return err }(),
		"create":       s.CreateSpace(bg, child),
		"update": func() error {
			_, err := s.UpdateSpace(bg, x.acct, child.Slug, "x", keep)
			return err
		}(),
		"setting": func() error { _, err := s.NonAdminCreation(bg, x.acct); return err }(),
		"toggle":  func() error { _, err := s.ToggleNonAdminCreation(bg, x.acct); return err }(),
		"delete":  s.DeleteSpace(bg, x.acct, child.Slug, allow),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}

// TestAccSpaceUpdate checks that an update writes a space's fields, a
// refused one writes nothing, and the non-admin creation setting reads
// false until toggled.
func TestAccSpaceUpdate(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "raviga")
	tr := treeOf(x.s)
	dev, err := tr.Create(ctx, x.acct, "raviga", space.Space{Name: "Dev", Labels: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.svc.Update(ctx, admin(x.acct), dev.Slug, space.LegacySlug, space.Space{
		Name: "Development", Description: "shared", InheritEntities: true,
	})
	if err != nil || got.Name != "Development" || got.ParentID == dev.ParentID {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	read, err := x.s.Space(ctx, x.acct, dev.Slug)
	if err != nil || read.Name != "Development" || read.Description != "shared" ||
		!read.InheritEntities || len(read.Labels) != 0 || read.Labels == nil ||
		read.ParentID != got.ParentID || read.Slug != dev.Slug {
		t.Errorf("after the update: %+v, %v", read, err)
	}
	if _, err := x.s.UpdateSpace(ctx, x.acct, dev.Slug, "",
		func(context.Context, space.Space, space.Space, *accessapi.SpaceMove) (space.Space, error) {
			return space.Space{}, errors.New("refused")
		}); err == nil {
		t.Error("a refused update succeeded")
	}
	if again, _ := x.s.Space(ctx, x.acct, dev.Slug); again.Name != "Development" {
		t.Errorf("a refused update wrote %+v", again)
	}

	for _, want := range []bool{false, true, false} {
		if on, err := tr.svc.NonAdminCreation(ctx, x.acct); err != nil || on != want {
			t.Errorf("NonAdminCreation = %t, %v; want %t", on, err, want)
		}
		if _, err := tr.svc.ToggleNonAdminCreation(ctx, admin(x.acct)); err != nil {
			t.Fatal(err)
		}
	}
	none := kernel.NewAccountID()
	if _, err := x.s.NonAdminCreation(ctx, none); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("the setting of no account = %v", err)
	}
	if _, err := x.s.ToggleNonAdminCreation(ctx, none); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("toggling the setting of no account = %v", err)
	}
	if _, err := x.s.UpdateSpace(
		ctx,
		x.acct,
		dev.Slug,
		"nope",
		nil,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("an update below a missing space = %v", err)
	}
}
