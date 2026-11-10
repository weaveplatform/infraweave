package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// bindingOf is a binding of x's account of the actor of kind id to the
// role slug in the space slug, made as origin.
func bindingOf(
	t *testing.T,
	x seeded,
	kind binding.ActorKind,
	id kernel.ULID,
	slug, space string,
	origin binding.Origin,
) binding.Binding {
	t.Helper()
	ctx := t.Context()
	r, err := x.s.RoleID(ctx, x.acct, slug)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := x.s.SpaceID(ctx, x.acct, space)
	if err != nil {
		t.Fatal(err)
	}
	return binding.Binding{
		AccountID: x.acct,
		ID:        kernel.NewULID(),
		ActorKind: kind,
		ActorID:   id,
		RoleID:    r,
		SpaceID:   sp,
		Origin:    origin,
		CreatedBy: "test",
		Version:   1,
	}
}

func ids(ls []binding.Listed) []kernel.ULID {
	out := make([]kernel.ULID, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.ID)
	}
	return out
}

// TestAccBindings records, reads, lists, moves, replaces and deletes
// bindings, refusing a duplicate on create or move, each write raising
// the account's authorization version.
func TestAccBindings(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "piedpiper")
	admit(t, x, x.member)

	for kind, id := range map[binding.ActorKind]kernel.ULID{
		binding.ActorUser: x.member.ULID, binding.ActorAPIKey: x.key,
	} {
		if held, err := x.s.HasActor(ctx, x.acct, kind, id); err != nil || !held {
			t.Errorf("%s held = %t, %v", kind, held, err)
		}
		if held, err := x.s.HasActor(ctx, x.acct, kind, kernel.NewULID()); err != nil || held {
			t.Errorf("another %s held = %t, %v", kind, held, err)
		}
	}
	if held, err := x.s.HasActor(
		ctx,
		x.acct,
		binding.ActorStack,
		kernel.NewULID(),
	); err != nil ||
		held {
		t.Errorf("a stack held = %t, %v", held, err)
	}
	if _, err := x.s.RoleID(ctx, x.acct, "nope"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("RoleID of none = %v", err)
	}
	if _, err := x.s.SpaceID(ctx, x.acct, "nope"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("SpaceID of none = %v", err)
	}

	was := authzVersion(t, x, x.acct)
	reader := bindingOf(t, x, binding.ActorUser, x.member.ULID, role.SpaceReader, "piedpiper",
		binding.OriginBinding)
	writer := bindingOf(t, x, binding.ActorUser, x.member.ULID, role.SpaceWriter, "legacy",
		binding.OriginAccessRule)
	got, err := x.s.CreateBindings(ctx, x.acct, []binding.Binding{writer, reader})
	if err != nil || len(got) != 2 || got[0].ID != writer.ID || got[1].ID != reader.ID ||
		got[0].Role != role.SpaceWriter || got[0].Space != "legacy" || got[1].Version != 1 ||
		got[0].Origin != binding.OriginAccessRule {
		t.Fatalf("create = %+v, %v", got, err)
	}
	if v := authzVersion(t, x, x.acct); v != was+1 {
		t.Errorf("authz_version = %d, want %d", v, was+1)
	}
	again := reader
	again.ID = kernel.NewULID()
	fresh := bindingOf(t, x, binding.ActorUser, x.member.ULID, role.SpaceAdmin, "legacy",
		binding.OriginBinding)
	_, err = x.s.CreateBindings(ctx, x.acct, []binding.Binding{fresh, again})
	if d, ok := errors.AsType[*binding.Duplicate](err); !ok || d.Index != 1 {
		t.Errorf("a duplicate = %v", err)
	}
	if _, err := x.s.Binding(
		ctx,
		x.acct,
		binding.ActorUser,
		fresh.ID,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("a refused batch wrote its first = %v", err)
	}

	l, err := x.s.Binding(ctx, x.acct, binding.ActorUser, reader.ID)
	if err != nil || l.Role != role.SpaceReader || l.Space != "piedpiper" || l.CreatedBy != "test" {
		t.Errorf("binding = %+v, %v", l, err)
	}
	if _, err := x.s.Binding(
		ctx,
		x.acct,
		binding.ActorAPIKey,
		reader.ID,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("binding of another kind = %v", err)
	}
	mine, err := x.s.ActorBindings(ctx, x.acct, binding.ActorUser, x.member.ULID)
	if err != nil || len(mine) != 2 {
		t.Errorf("actor's = %+v, %v", mine, err)
	}
	inTop, err := x.s.SpaceBindings(ctx, x.acct, x.acct.ULID)
	if err != nil || !slices.Contains(ids(inTop), reader.ID) ||
		slices.Contains(ids(inTop), writer.ID) {
		t.Errorf("top space's = %+v, %v", inTop, err)
	}

	moved, err := x.s.UpdateBinding(ctx, x.acct, binding.ActorUser, reader.ID,
		func(l binding.Listed) (binding.Binding, error) {
			b := l.Binding
			b.RoleID = fresh.RoleID
			return b, nil
		})
	if err != nil || moved.Role != role.SpaceAdmin || moved.Version != 2 {
		t.Errorf("move = %+v, %v", moved, err)
	}
	_, err = x.s.UpdateBinding(ctx, x.acct, binding.ActorUser, reader.ID,
		func(l binding.Listed) (binding.Binding, error) {
			b := l.Binding
			b.RoleID, b.SpaceID = writer.RoleID, writer.SpaceID
			return b, nil
		})
	if _, ok := errors.AsType[*binding.Duplicate](err); !ok {
		t.Errorf("a move onto a held binding = %v", err)
	}
	if _, err := x.s.UpdateBinding(ctx, x.acct, binding.ActorUser, reader.ID,
		func(binding.Listed) (binding.Binding, error) { return binding.Binding{}, kernel.ErrStale },
	); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a refused move = %v", err)
	}
	if _, err := x.s.UpdateBinding(
		ctx,
		x.acct,
		binding.ActorUser,
		kernel.NewULID(),
		nil,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("a move of none = %v", err)
	}

	rule := bindingOf(t, x, binding.ActorUser, x.member.ULID, role.SpaceReader, "legacy",
		binding.OriginAccessRule)
	if err := x.s.ReplaceAccessRules(ctx, x.acct, binding.ActorUser, x.member.ULID,
		[]binding.Binding{rule}); err != nil {
		t.Fatal(err)
	}
	mine, _ = x.s.ActorBindings(ctx, x.acct, binding.ActorUser, x.member.ULID)
	if got := ids(
		mine,
	); len(got) != 2 || !slices.Contains(got, reader.ID) ||
		!slices.Contains(got, rule.ID) {
		t.Errorf("after replacing the rules = %+v", mine)
	}
	dup := rule
	dup.ID = kernel.NewULID()
	if err := x.s.ReplaceAccessRules(ctx, x.acct, binding.ActorUser, x.member.ULID,
		[]binding.Binding{rule, dup}); err == nil {
		t.Error("replacing with a duplicate succeeded")
	}

	if _, err := x.s.DeleteBinding(ctx, x.acct, binding.ActorUser, rule.ID,
		func(binding.Listed) error { return kernel.ErrStale }); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a refused delete = %v", err)
	}
	gone, err := x.s.DeleteBinding(ctx, x.acct, binding.ActorUser, rule.ID,
		func(binding.Listed) error { return nil })
	if err != nil || gone.ID != rule.ID {
		t.Errorf("delete = %+v, %v", gone, err)
	}
	if _, err := x.s.DeleteBinding(ctx, x.acct, binding.ActorUser, rule.ID, nil); !errors.Is(err,
		port.ErrNotFound) {
		t.Errorf("a delete of none = %v", err)
	}
	was = authzVersion(t, x, x.acct)
	if err := x.s.DeleteActorBindings(ctx, x.acct, binding.ActorUser, x.member.ULID); err != nil {
		t.Fatal(err)
	}
	if mine, _ := x.s.ActorBindings(
		ctx,
		x.acct,
		binding.ActorUser,
		x.member.ULID,
	); len(
		mine,
	) != 0 ||
		authzVersion(t, x, x.acct) != was+1 {
		t.Errorf("after deleting the actor's = %+v", mine)
	}
}

// TestAccAtomic checks that a write fn makes through Atomic's context is
// undone with fn's failure, and kept with its success.
func TestAccAtomic(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "endframe")
	b := bindingOf(
		t,
		x,
		binding.ActorAPIKey,
		x.key,
		role.SpaceReader,
		"endframe",
		binding.OriginBinding,
	)
	fail := errors.New("fail")
	err := x.s.Atomic(ctx, func(ctx context.Context) error {
		if _, err := x.s.CreateBindings(ctx, x.acct, []binding.Binding{b}); err != nil {
			return err
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatalf("atomic = %v", err)
	}
	if _, err := x.s.Binding(
		ctx,
		x.acct,
		binding.ActorAPIKey,
		b.ID,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("a failed fn's write stayed: %v", err)
	}
	if err := x.s.Atomic(ctx, func(ctx context.Context) error {
		_, err := x.s.CreateBindings(ctx, x.acct, []binding.Binding{b})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.Binding(ctx, x.acct, binding.ActorAPIKey, b.ID); err != nil {
		t.Errorf("a successful fn's write = %v", err)
	}
}

// TestAccRemovedActorsUnbound checks that removing a managed user,
// deleting a group or deleting an API key deletes its bindings and no
// other actor's.
func TestAccRemovedActorsUnbound(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "raviga")
	admit(t, x, x.member)
	g := group.Group{AccountID: x.acct, ID: kernel.NewULID(), Name: "ops", CreatedAt: spacesAt}
	if _, err := x.s.CreateGroup(ctx, g, onTrail(x, group.ActionCreate)); err != nil {
		t.Fatal(err)
	}
	other := kernel.NewULID()
	if _, err := x.s.CreateBindings(ctx, x.acct, []binding.Binding{
		bindingOf(
			t,
			x,
			binding.ActorUser,
			x.member.ULID,
			role.SpaceReader,
			"raviga",
			binding.OriginBinding,
		),
		bindingOf(
			t,
			x,
			binding.ActorGroup,
			g.ID,
			role.SpaceReader,
			"raviga",
			binding.OriginBinding,
		),
		bindingOf(
			t,
			x,
			binding.ActorAPIKey,
			x.key,
			role.SpaceReader,
			"raviga",
			binding.OriginBinding,
		),
		bindingOf(
			t,
			x,
			binding.ActorGroup,
			other,
			role.SpaceReader,
			"raviga",
			binding.OriginBinding,
		),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.Remove(
		ctx,
		x.acct,
		x.member,
		kernel.Expect{},
		onTrail(x, user.ActionDelete),
	); err != nil {
		t.Fatal(err)
	}
	if err := x.s.DeleteGroup(
		ctx,
		x.acct,
		g.ID,
		kernel.Expect{},
		onTrail(x, group.ActionDelete),
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.DeleteAPIKey(ctx, x.acct, x.key, kernel.Expect{}); err != nil {
		t.Fatal(err)
	}
	for kind, id := range map[binding.ActorKind]kernel.ULID{
		binding.ActorUser: x.member.ULID, binding.ActorGroup: g.ID, binding.ActorAPIKey: x.key,
	} {
		if left, err := x.s.ActorBindings(ctx, x.acct, kind, id); err != nil || len(left) != 0 {
			t.Errorf("removed %s's = %+v, %v", kind, left, err)
		}
	}
	if left, err := x.s.ActorBindings(
		ctx,
		x.acct,
		binding.ActorGroup,
		other,
	); err != nil ||
		len(left) != 1 {
		t.Errorf("another group's = %+v, %v", left, err)
	}
}

// TestAccBindingStoreUnavailable checks that each binding read and write
// fails, never as not-found, when a write fails within its transaction or
// PostgreSQL is closed.
func TestAccBindingStoreUnavailable(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "bachmanity")
	b := bindingOf(
		t,
		x,
		binding.ActorAPIKey,
		x.key,
		role.SpaceReader,
		"bachmanity",
		binding.OriginBinding,
	)
	rule := bindingOf(
		t,
		x,
		binding.ActorAPIKey,
		x.key,
		role.SpaceAdmin,
		"legacy",
		binding.OriginAccessRule,
	)
	if _, err := x.s.CreateBindings(ctx, x.acct, []binding.Binding{b, rule}); err != nil {
		t.Fatal(err)
	}
	// Triggers of this test's own database fail each write of a binding
	// created by refused, and every update and delete.
	if _, err := x.db.Exec(ctx, `
		CREATE FUNCTION refuse() RETURNS trigger LANGUAGE plpgsql AS
			$$BEGIN RAISE EXCEPTION 'refused'; END$$;
		CREATE TRIGGER refuse_insert BEFORE INSERT ON role_bindings FOR EACH ROW
			WHEN (NEW.created_by = 'refused') EXECUTE FUNCTION refuse();
		CREATE TRIGGER refuse_update BEFORE UPDATE ON role_bindings FOR EACH ROW
			EXECUTE FUNCTION refuse();
		CREATE TRIGGER refuse_delete BEFORE DELETE ON role_bindings FOR EACH ROW
			EXECUTE FUNCTION refuse();`); err != nil {
		t.Fatal(err)
	}
	refused := bindingOf(t, x, binding.ActorAPIKey, x.key, role.SpaceWriter, "bachmanity",
		binding.OriginAccessRule)
	refused.CreatedBy = "refused"
	keep := func(l binding.Listed) (binding.Binding, error) { return l.Binding, nil }
	accept := func(binding.Listed) error { return nil }
	for name, err := range map[string]error{
		"create":  func() error { _, err := x.s.CreateBindings(ctx, x.acct, []binding.Binding{refused}); return err }(),
		"update":  func() error { _, err := x.s.UpdateBinding(ctx, x.acct, b.ActorKind, b.ID, keep); return err }(),
		"delete":  func() error { _, err := x.s.DeleteBinding(ctx, x.acct, b.ActorKind, b.ID, accept); return err }(),
		"replace": x.s.ReplaceAccessRules(ctx, x.acct, b.ActorKind, x.key, nil),
		"actor":   x.s.DeleteActorBindings(ctx, x.acct, b.ActorKind, x.key),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s failing in its transaction = %v", name, err)
		}
	}
	if _, err := x.db.Exec(ctx, `DROP TRIGGER refuse_delete ON role_bindings`); err != nil {
		t.Fatal(err)
	}
	if err := x.s.ReplaceAccessRules(
		ctx,
		x.acct,
		b.ActorKind,
		x.key,
		[]binding.Binding{refused},
	); err == nil {
		t.Error("replace inserting a refused rule succeeded")
	}

	p := pool(t)
	s := pg.New(p)
	p.Close()
	bg := context.Background()
	for name, err := range map[string]error{
		"role":  func() error { _, err := s.RoleID(bg, x.acct, "space-reader"); return err }(),
		"space": func() error { _, err := s.SpaceID(bg, x.acct, "bachmanity"); return err }(),
		"actor": func() error { _, err := s.HasActor(bg, x.acct, b.ActorKind, x.key); return err }(),
		"read":  func() error { _, err := s.Binding(bg, x.acct, b.ActorKind, b.ID); return err }(),
		"list":  func() error { _, err := s.ActorBindings(bg, x.acct, b.ActorKind, x.key); return err }(),
		"space's": func() error {
			_, err := s.SpaceBindings(bg, x.acct, x.acct.ULID)
			return err
		}(),
		"create":  func() error { _, err := s.CreateBindings(bg, x.acct, []binding.Binding{b}); return err }(),
		"update":  func() error { _, err := s.UpdateBinding(bg, x.acct, b.ActorKind, b.ID, keep); return err }(),
		"delete":  func() error { _, err := s.DeleteBinding(bg, x.acct, b.ActorKind, b.ID, accept); return err }(),
		"replace": s.ReplaceAccessRules(bg, x.acct, b.ActorKind, x.key, nil),
		"unbind":  s.DeleteActorBindings(bg, x.acct, b.ActorKind, x.key),
		"atomic":  s.Atomic(bg, func(context.Context) error { return nil }),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s with PostgreSQL closed = %v", name, err)
		}
	}
}
