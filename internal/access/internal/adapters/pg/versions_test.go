package pg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// stale is an expect naming a version no row reaches in these tests.
var stale = kernel.ExpectVersions(99)

// TestAccSpaceVersions checks that a space starts at version 1, that a
// change raises it once, a move included, and that an update or delete
// whose expect the version read under the tree's lock fails is
// kernel.ErrStale and changes nothing.
func TestAccSpaceVersions(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "versioned-spaces")
	tr := treeOf(x.s)
	dev, err := tr.Create(ctx, x.acct, "versioned-spaces", space.Space{
		Name: "Dev", Labels: []string{"b", "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func() space.Space {
		t.Helper()
		got, err := x.s.Space(ctx, x.acct, dev.Slug)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read(); got.Version != 1 || !slices.Equal(got.Labels, []string{"b", "a"}) {
		t.Fatalf("a new space = %+v", got)
	}

	p := admin(x.acct)
	if _, err := tr.svc.Update(ctx, p, dev.Slug, "versioned-spaces", stale,
		space.Space{Name: "Changed"}); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale update = %v", err)
	}
	if _, err := tr.svc.Delete(ctx, p, dev.Slug, stale); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale delete = %v", err)
	}
	if got := read(); got.Name != "Dev" || got.Version != 1 {
		t.Errorf("after the stale writes = %+v", got)
	}

	got, err := tr.svc.Update(ctx, p, dev.Slug, "versioned-spaces", kernel.ExpectVersions(1),
		space.Space{Name: "Development", Labels: []string{"c"}})
	if err != nil || got.Version != 2 || read().Version != 2 {
		t.Errorf("an update naming the version = %+v, %v", got, err)
	}
	moved, err := tr.svc.Update(ctx, p, dev.Slug, space.LegacySlug, kernel.ExpectAny(),
		space.Space{Name: "Development"})
	if err != nil || moved.Version != 3 || read().ParentID != moved.ParentID {
		t.Errorf("a move = %+v, %v", moved, err)
	}
	if _, err := tr.svc.Delete(ctx, p, dev.Slug, kernel.ExpectVersions(3)); err != nil {
		t.Errorf("a delete naming the version = %v", err)
	}
	if _, err := x.s.Space(ctx, x.acct, dev.Slug); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("the deleted space reads %v", err)
	}
}

// TestAccAPIKeyVersions checks that a key starts at version 1, that each
// change but a use raises it, and that an update or delete whose expect
// fails is kernel.ErrStale and changes nothing.
func TestAccAPIKeyVersions(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "versioned-keys")
	h, _ := apikey.Hash(apikey.NewSecret())
	k := apikey.Key{
		AccountID: x.acct, ID: kernel.NewULID(), Name: "deploy", Type: apikey.TypeSecret,
		SecretHash: h, Creator: "alice", CreatedAt: time.Now().UTC(),
	}
	if err := x.s.CreateAPIKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	version := func() int64 {
		t.Helper()
		got, err := x.s.APIKey(ctx, k.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Version
	}
	if v := version(); v != 1 {
		t.Fatalf("a new key's version = %d", v)
	}

	name := "renamed"
	if _, err := x.s.UpdateAPIKey(ctx, x.acct, k.ID, stale,
		apikey.Change{Name: &name}); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale update = %v", err)
	}
	if _, _, err := x.s.DeleteAPIKey(ctx, x.acct, k.ID, stale); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale delete = %v", err)
	}
	if got, _ := x.s.APIKey(ctx, k.ID); got.Name != "deploy" || !got.DeletedAt.IsZero() ||
		got.Version != 1 {
		t.Errorf("after the stale writes = %+v", got)
	}
	if _, err := x.s.UpdateAPIKey(ctx, x.acct, kernel.NewULID(), kernel.ExpectAny(),
		apikey.Change{}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an update of no key = %v", err)
	}

	got, err := x.s.UpdateAPIKey(ctx, x.acct, k.ID, kernel.ExpectVersions(1),
		apikey.Change{Name: &name})
	if err != nil || got.Name != "renamed" || got.Version != 2 {
		t.Errorf("an update naming the version = %+v, %v", got, err)
	}
	if err := x.s.TouchAPIKey(ctx, k.ID); err != nil || version() != 2 {
		t.Errorf("a use raised the version to %d, %v", version(), err)
	}
	if got, _, err := x.s.ResetAPIKey(ctx, x.acct, k.ID, h); err != nil || got.Version != 3 {
		t.Errorf("a reset = %+v, %v", got, err)
	}
	if got, _, err := x.s.DisableAPIKey(ctx, x.acct, k.ID, apikey.DisabledByAdmin); err != nil ||
		got.Version != 4 {
		t.Errorf("a disable = %+v, %v", got, err)
	}
	if got, _, err := x.s.DeleteAPIKey(ctx, x.acct, k.ID, kernel.ExpectVersions(4)); err != nil ||
		got.DeletedAt.IsZero() || got.Version != 5 {
		t.Errorf("a delete naming the version = %+v, %v", got, err)
	}
}

// TestAccManagedVersions checks that a managed user and a group start at
// version 1, that each change raises it, and that a change or removal
// whose expect fails is kernel.ErrStale and changes nothing; removing a
// managed user raises the version of the personal keys it disables.
func TestAccManagedVersions(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "versioned-users")
	admit(t, x, x.member)
	alice := func() user.User {
		t.Helper()
		u, err := x.s.User(ctx, x.acct, x.member)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	if u := alice(); u.Version != 1 {
		t.Fatalf("an admission's version = %d", u.Version)
	}
	if _, err := x.s.SetSlackMemberID(ctx, x.acct, x.member, stale, "U1",
		onTrail(x, user.ActionUpdate)); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale update = %v", err)
	}
	if _, err := x.s.Remove(ctx, x.acct, x.member, stale,
		onTrail(x, user.ActionDelete)); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale removal = %v", err)
	}
	if u := alice(); u.SlackMemberID != "" || u.Version != 1 {
		t.Errorf("after the stale writes = %+v", u)
	}
	if n := accountTrail(t, x, user.ActionUpdate) + accountTrail(t, x, user.ActionDelete); n != 0 {
		t.Errorf("the stale writes left %d trail entries", n)
	}
	u, err := x.s.SetSlackMemberID(ctx, x.acct, x.member, kernel.ExpectVersions(1), "U1",
		onTrail(x, user.ActionUpdate))
	if err != nil || u.SlackMemberID != "U1" || u.Version != 2 {
		t.Errorf("an update naming the version = %+v, %v", u, err)
	}

	g := group.Group{
		AccountID: x.acct, ID: kernel.NewULID(), Name: "ops", CreatedAt: time.Now().UTC(),
	}
	if g, err = x.s.CreateGroup(ctx, g, onTrail(x, group.ActionCreate)); err != nil ||
		g.Version != 1 {
		t.Fatalf("a new group = %+v, %v", g, err)
	}
	if _, err := x.s.SetDescription(ctx, x.acct, g.ID, stale, "x",
		onTrail(x, group.ActionUpdate)); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale description = %v", err)
	}
	if err := x.s.DeleteGroup(ctx, x.acct, g.ID, stale,
		onTrail(x, group.ActionDelete)); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale group delete = %v", err)
	}
	if got, err := x.s.Group(ctx, x.acct, g.ID); err != nil || got.Description != "" ||
		got.Version != 1 {
		t.Errorf("after the stale writes = %+v, %v", got, err)
	}
	if got, err := x.s.SetDescription(ctx, x.acct, g.ID, kernel.ExpectVersions(1), "people",
		onTrail(x, group.ActionUpdate)); err != nil || got.Description != "people" ||
		got.Version != 2 {
		t.Errorf("a description naming the version = %+v, %v", got, err)
	}
	if err := x.s.DeleteGroup(ctx, x.acct, g.ID, kernel.ExpectVersions(2),
		onTrail(x, group.ActionDelete)); err != nil {
		t.Errorf("a group delete naming the version = %v", err)
	}

	k, err := x.s.CreatePersonalAPIKey(ctx, personal(x, x.member))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.Remove(ctx, x.acct, x.member, kernel.ExpectVersions(2),
		onTrail(x, user.ActionDelete)); err != nil {
		t.Fatalf("a removal naming the version = %v", err)
	}
	if got, err := x.s.APIKey(ctx, k.ID); err != nil || got.DisabledAt.IsZero() ||
		got.Version != 2 {
		t.Errorf("the removed member's personal key = %+v, %v", got, err)
	}
}
