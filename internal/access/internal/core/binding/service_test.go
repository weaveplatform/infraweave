package binding_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var errDown = errors.New("down")

// store keeps one account's bindings in memory beside fixed roles, spaces
// and actors; fail names the methods that answer errDown.
type store struct {
	roles, spaces map[string]kernel.ULID
	actors        map[kernel.ULID]bool
	all           []binding.Listed
	fail          map[string]bool
}

func newStore() *store {
	return &store{
		roles: map[string]kernel.ULID{
			"space-reader": kernel.NewULID(),
			"space-admin":  kernel.NewULID(),
		},
		spaces: map[string]kernel.ULID{"top": kernel.NewULID(), "team": kernel.NewULID()},
		actors: map[kernel.ULID]bool{},
		fail:   map[string]bool{},
	}
}

func (s *store) down(method string) error {
	if s.fail[method] {
		return errDown
	}
	return nil
}

func lookup(m map[string]kernel.ULID, slug string) (kernel.ULID, error) {
	id, ok := m[slug]
	if !ok {
		return kernel.ULID{}, port.ErrNotFound
	}
	return id, nil
}

func (s *store) RoleID(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
	if err := s.down("RoleID"); err != nil {
		return kernel.ULID{}, err
	}
	return lookup(s.roles, slug)
}

func (s *store) SpaceID(_ context.Context, _ kernel.AccountID, slug string) (kernel.ULID, error) {
	if err := s.down("SpaceID"); err != nil {
		return kernel.ULID{}, err
	}
	return lookup(s.spaces, slug)
}

func (s *store) HasActor(
	_ context.Context,
	_ kernel.AccountID,
	_ binding.ActorKind,
	id kernel.ULID,
) (bool, error) {
	return s.actors[id], s.down("HasActor")
}

func (s *store) Binding(
	_ context.Context,
	_ kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) (binding.Listed, error) {
	for _, l := range s.all {
		if l.ID == id && l.ActorKind == kind {
			return l, s.down("Binding")
		}
	}
	return binding.Listed{}, port.ErrNotFound
}

func (s *store) ActorBindings(
	_ context.Context,
	_ kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
) ([]binding.Listed, error) {
	var out []binding.Listed
	for _, l := range s.all {
		if l.ActorKind == kind && l.ActorID == id {
			out = append(out, l)
		}
	}
	return out, s.down("ActorBindings")
}

func (s *store) SpaceBindings(
	_ context.Context,
	_ kernel.AccountID,
	id kernel.ULID,
) ([]binding.Listed, error) {
	var out []binding.Listed
	for _, l := range s.all {
		if l.SpaceID == id {
			out = append(out, l)
		}
	}
	return out, s.down("SpaceBindings")
}

func (s *store) CreateBindings(
	_ context.Context,
	_ kernel.AccountID,
	bs []binding.Binding,
) ([]binding.Listed, error) {
	if err := s.down("CreateBindings"); err != nil {
		return nil, err
	}
	out := make([]binding.Listed, 0, len(bs))
	for _, b := range bs {
		out = append(out, binding.Listed{Binding: b})
	}
	s.all = append(s.all, out...)
	return out, nil
}

func (s *store) UpdateBinding(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	edit func(binding.Listed) (binding.Binding, error),
) (binding.Listed, error) {
	if err := s.down("UpdateBinding"); err != nil {
		return binding.Listed{}, err
	}
	cur, err := s.Binding(ctx, acct, kind, id)
	if err != nil {
		return binding.Listed{}, err
	}
	b, err := edit(cur)
	if err != nil {
		return binding.Listed{}, err
	}
	b.Version++
	return binding.Listed{Binding: b}, nil
}

func (s *store) DeleteBinding(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	check func(binding.Listed) error,
) (binding.Listed, error) {
	cur, err := s.Binding(ctx, acct, kind, id)
	if err != nil {
		return binding.Listed{}, err
	}
	if err := check(cur); err != nil {
		return binding.Listed{}, err
	}
	s.all = slices.DeleteFunc(s.all, func(l binding.Listed) bool { return l.ID == id })
	return cur, s.down("DeleteBinding")
}

func (s *store) ReplaceAccessRules(
	_ context.Context,
	_ kernel.AccountID,
	_ binding.ActorKind,
	_ kernel.ULID,
	_ []binding.Binding,
) error {
	return s.down("ReplaceAccessRules")
}

func (s *store) DeleteActorBindings(
	_ context.Context,
	_ kernel.AccountID,
	_ binding.ActorKind,
	_ kernel.ULID,
) error {
	return s.down("DeleteActorBindings")
}

// grants holds actions by space; err fails every decision.
type grants struct {
	on  map[kernel.ULID][]accessapi.Action
	err error
}

func (g *grants) Authorize(
	_ context.Context,
	_ kernel.Principal,
	a accessapi.Action,
	space kernel.ULID,
) (bool, error) {
	return slices.Contains(g.on[space], a), g.err
}

func (g *grants) Reachable(
	context.Context,
	kernel.Principal,
	accessapi.Action,
) ([]kernel.ULID, error) {
	return nil, g.err
}

func (g *grants) Holds(context.Context, kernel.Principal, accessapi.Action) (bool, error) {
	return false, g.err
}

func (g *grants) Actions(
	context.Context,
	kernel.Principal,
	kernel.ULID,
) ([]accessapi.Action, error) {
	return nil, g.err
}

// fixture is a service over a store whose caller administers the top
// space and reads the team space, with the drops it made.
type fixture struct {
	st    *store
	authz *grants
	svc   *binding.Service
	p     kernel.Principal
	drops int
}

func newFixture() *fixture {
	f := &fixture{st: newStore(), p: kernel.Principal{AccountID: kernel.NewAccountID()}}
	admin := []accessapi.Action{accessapi.ActionSpaceRead, accessapi.ActionSpaceAdmin}
	f.authz = &grants{on: map[kernel.ULID][]accessapi.Action{
		f.st.spaces["top"]:  admin,
		f.st.spaces["team"]: {accessapi.ActionSpaceRead},
	}}
	f.svc = binding.NewService(binding.Deps{
		Store: f.st, Authz: f.authz, Drop: func(kernel.AccountID) { f.drops++ },
	})
	return f
}

var fields = binding.Fields{Actor: "userID", Role: "roleID", Space: "spaceID"}

func pointers(t *testing.T, err error) []string {
	t.Helper()
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok {
		t.Fatalf("err = %v, want a refusal", err)
	}
	out := make([]string, 0, len(inv.Errors))
	for _, e := range inv.Errors {
		out = append(out, e.Pointer)
	}
	return out
}

// TestUnitPrepare checks that each element's actor, role and space are
// refused at their pointers, and that a store or authorizer failure ends
// the check.
func TestUnitPrepare(t *testing.T) {
	f := newFixture()
	bob := kernel.NewULID()
	f.st.actors[bob] = true
	ctx := t.Context()
	bs, err := f.svc.Prepare(ctx, f.p, binding.ActorUser, binding.OriginAccessRule, "/b", fields,
		[]binding.Request{{Actor: bob.String(), Role: "space-reader", Space: "top"}})
	if err != nil || len(bs) != 1 || bs[0].ActorID != bob ||
		bs[0].Origin != binding.OriginAccessRule ||
		bs[0].SpaceID != f.st.spaces["top"] ||
		bs[0].Version != 1 {
		t.Fatalf("prepare = %+v, %v", bs, err)
	}
	_, err = f.svc.Prepare(
		ctx,
		f.p,
		binding.ActorUser,
		binding.OriginBinding,
		"/b",
		fields,
		[]binding.Request{
			{Actor: "x", Role: "nope", Space: "nowhere"},
			{Actor: kernel.NewULID().String(), Role: "space-reader", Space: "team"},
		},
	)
	if got := pointers(t, err); !slices.Equal(got, []string{
		"/b/0/userID", "/b/0/roleID", "/b/0/spaceID", "/b/1/userID", "/b/1/spaceID",
	}) {
		t.Errorf("refused = %v", got)
	}
	for _, method := range []string{"RoleID", "SpaceID", "HasActor"} {
		f.st.fail[method] = true
		if _, err := f.svc.Prepare(
			ctx,
			f.p,
			binding.ActorUser,
			binding.OriginBinding,
			"",
			fields,
			[]binding.Request{
				{Actor: bob.String(), Role: "space-reader", Space: "top"},
			},
		); !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", method, err)
		}
		delete(f.st.fail, method)
	}
	f.authz.err = errDown
	if _, err := f.svc.Prepare(ctx, f.p, binding.ActorUser, binding.OriginBinding, "", fields,
		[]binding.Request{{Role: "space-reader", Space: "top"}}); !errors.Is(err, errDown) {
		t.Errorf("authorizer down = %v", err)
	}
	// The space is readable, so only the admin decision fails.
	f.authz = &grants{on: map[kernel.ULID][]accessapi.Action{
		f.st.spaces["top"]: {accessapi.ActionSpaceRead},
	}}
	f.svc = binding.NewService(
		binding.Deps{Store: f.st, Authz: failAdmin{f.authz}, Drop: func(kernel.AccountID) {}},
	)
	if _, err := f.svc.Prepare(ctx, f.p, binding.ActorUser, binding.OriginBinding, "", fields,
		[]binding.Request{{Role: "space-reader", Space: "top"}}); !errors.Is(err, errDown) {
		t.Errorf("admin decision down = %v", err)
	}
}

// failAdmin fails only SPACE_ADMIN decisions.
type failAdmin struct{ *grants }

func (g failAdmin) Authorize(
	ctx context.Context,
	p kernel.Principal,
	a accessapi.Action,
	space kernel.ULID,
) (bool, error) {
	if a == accessapi.ActionSpaceAdmin {
		return false, errDown
	}
	return g.grants.Authorize(ctx, p, a, space)
}

// TestUnitServiceWrites creates, binds, moves, replaces and deletes
// bindings, dropping the account's authorization after each write and
// never after a failed one.
func TestUnitServiceWrites(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	bob := kernel.NewULID()
	f.st.actors[bob] = true
	acct := f.p.AccountID

	_, err := f.svc.Create(ctx, f.p, binding.ActorUser, "", fields,
		[]binding.Request{{Actor: bob.String(), Role: "nope", Space: "top"}})
	if got := pointers(t, err); !slices.Equal(got, []string{"/roleID"}) {
		t.Errorf("single refusal = %v", got)
	}
	_, err = f.svc.Create(ctx, f.p, binding.ActorUser, "/bindings", fields,
		[]binding.Request{{Actor: bob.String(), Role: "nope", Space: "top"}})
	if got := pointers(t, err); !slices.Equal(got, []string{"/bindings/0/roleID"}) {
		t.Errorf("batch refusal = %v", got)
	}
	ls, err := f.svc.Create(ctx, f.p, binding.ActorUser, "", fields,
		[]binding.Request{{Actor: bob.String(), Role: "space-reader", Space: "top"}})
	if err != nil || len(ls) != 1 || f.drops != 1 {
		t.Fatalf("create = %+v, %v, drops %d", ls, err, f.drops)
	}
	f.st.fail["CreateBindings"] = true
	if _, err := f.svc.Create(
		ctx,
		f.p,
		binding.ActorUser,
		"",
		fields,
		[]binding.Request{
			{Actor: bob.String(), Role: "space-admin", Space: "top"},
		},
	); !errors.Is(err, errDown) {
		t.Errorf("create down = %v", err)
	}
	if _, err := f.svc.Bind(
		ctx,
		acct,
		binding.ActorUser,
		bob,
		[]binding.Binding{{}},
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("bind down = %v", err)
	}
	delete(f.st.fail, "CreateBindings")
	if got, err := f.svc.Bind(ctx, acct, binding.ActorUser, bob, nil); err != nil || len(got) != 0 {
		t.Errorf("bind none = %+v, %v", got, err)
	}
	got, err := f.svc.Bind(
		ctx,
		acct,
		binding.ActorGroup,
		bob,
		[]binding.Binding{{ID: kernel.NewULID()}},
	)
	if err != nil || got[0].ActorKind != binding.ActorGroup || got[0].ActorID != bob ||
		f.drops != 2 {
		t.Errorf("bind = %+v, %v", got, err)
	}

	id := ls[0].ID.String()
	moved, err := f.svc.Update(ctx, f.p, binding.ActorUser, id, kernel.Expect{}, fields,
		binding.Request{Actor: bob.String(), Role: "space-admin", Space: "top"}, nil)
	if err != nil || moved.RoleID != f.st.roles["space-admin"] || moved.Version != 2 ||
		f.drops != 3 {
		t.Errorf("update = %+v, %v", moved, err)
	}
	if _, err := f.svc.Update(ctx, f.p, binding.ActorUser, id, kernel.ExpectVersions(9), fields,
		binding.Request{Role: "space-admin", Space: "top"}, nil); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("stale update = %v", err)
	}
	if _, err := f.svc.Update(ctx, f.p, binding.ActorUser, id, kernel.Expect{}, fields,
		binding.Request{Role: "space-admin", Space: "top"},
		func(binding.Listed) error { return errDown }); !errors.Is(err, errDown) {
		t.Errorf("refused update = %v", err)
	}
	_, err = f.svc.Update(ctx, f.p, binding.ActorUser, id, kernel.Expect{}, fields,
		binding.Request{Actor: kernel.NewULID().String(), Role: "space-admin", Space: "top"}, nil)
	if got := pointers(t, err); !slices.Equal(got, []string{"/userID"}) {
		t.Errorf("another actor = %v", got)
	}
	_, err = f.svc.Update(ctx, f.p, binding.ActorUser, id, kernel.Expect{}, fields,
		binding.Request{Role: "space-admin", Space: "team"}, nil)
	if got := pointers(t, err); !slices.Equal(got, []string{"/spaceID"}) {
		t.Errorf("unadministered space = %v", got)
	}
	if _, err := f.svc.Update(ctx, f.p, binding.ActorUser, "nope", kernel.Expect{}, fields,
		binding.Request{}, nil); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("update of none = %v", err)
	}
	f.st.fail["UpdateBinding"] = true
	if _, err := f.svc.Update(ctx, f.p, binding.ActorUser, id, kernel.Expect{}, fields,
		binding.Request{Role: "space-admin", Space: "top"}, nil); !errors.Is(err, errDown) {
		t.Errorf("update down = %v", err)
	}
	delete(f.st.fail, "UpdateBinding")
	f.st.fail["RoleID"] = true
	if _, err := f.svc.Update(ctx, f.p, binding.ActorUser, id, kernel.Expect{}, fields,
		binding.Request{Role: "space-admin", Space: "top"}, nil); !errors.Is(err, errDown) {
		t.Errorf("update's check down = %v", err)
	}
	delete(f.st.fail, "RoleID")

	if err := f.svc.ReplaceRules(
		ctx,
		acct,
		binding.ActorUser,
		bob,
		[]binding.Binding{{}},
	); err != nil ||
		f.drops != 4 {
		t.Errorf("replace = %v", err)
	}
	if err := f.svc.Unbind(ctx, acct, binding.ActorUser, bob); err != nil || f.drops != 5 {
		t.Errorf("unbind = %v", err)
	}
	f.st.fail["ReplaceAccessRules"], f.st.fail["DeleteActorBindings"] = true, true
	if err := f.svc.ReplaceRules(ctx, acct, binding.ActorUser, bob, nil); !errors.Is(err, errDown) {
		t.Errorf("replace down = %v", err)
	}
	if err := f.svc.Unbind(ctx, acct, binding.ActorUser, bob); !errors.Is(err, errDown) {
		t.Errorf("unbind down = %v", err)
	}

	if _, err := f.svc.Delete(
		ctx,
		acct,
		binding.ActorUser,
		"nope",
		kernel.Expect{},
		nil,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("delete of none = %v", err)
	}
	if _, err := f.svc.Delete(
		ctx,
		acct,
		binding.ActorUser,
		id,
		kernel.ExpectVersions(9),
		nil,
	); !errors.Is(
		err,
		kernel.ErrStale,
	) {
		t.Errorf("stale delete = %v", err)
	}
	f.st.fail["DeleteBinding"] = true
	if _, err := f.svc.Delete(
		ctx,
		acct,
		binding.ActorUser,
		id,
		kernel.Expect{},
		nil,
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("delete down = %v", err)
	}
	if f.drops != 5 {
		t.Errorf("drops = %d, want 5", f.drops)
	}
}

// TestUnitServiceReads reads a binding, an actor's and a space's.
func TestUnitServiceReads(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	acct := f.p.AccountID
	bob := kernel.NewULID()
	f.st.actors[bob] = true
	l := binding.Listed{Binding: binding.Binding{
		ID:        kernel.NewULID(),
		ActorKind: binding.ActorUser,
		ActorID:   bob,
		SpaceID:   f.st.spaces["top"],
	}}
	f.st.all = []binding.Listed{l}

	if got, err := f.svc.Binding(
		ctx,
		acct,
		binding.ActorUser,
		l.ID.String(),
	); err != nil ||
		got.ID != l.ID {
		t.Errorf("binding = %+v, %v", got, err)
	}
	if _, err := f.svc.Binding(
		ctx,
		acct,
		binding.ActorUser,
		"nope",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("binding of none = %v", err)
	}
	if got, err := f.svc.Of(
		ctx,
		acct,
		binding.ActorUser,
		bob.String(),
	); err != nil ||
		len(got) != 1 {
		t.Errorf("of = %+v, %v", got, err)
	}
	if _, err := f.svc.Of(
		ctx,
		acct,
		binding.ActorUser,
		kernel.NewULID().String(),
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("of none = %v", err)
	}
	f.st.fail["HasActor"] = true
	if _, err := f.svc.Of(ctx, acct, binding.ActorUser, bob.String()); !errors.Is(err, errDown) {
		t.Errorf("of down = %v", err)
	}
	if got, err := f.svc.Held(ctx, acct, binding.ActorUser, bob); err != nil || len(got) != 1 {
		t.Errorf("held = %+v, %v", got, err)
	}
	if got, err := f.svc.InSpace(ctx, acct, f.st.spaces["top"]); err != nil || len(got) != 1 {
		t.Errorf("in space = %+v, %v", got, err)
	}
}

// TestUnitReleasable checks that replacing an actor's access rules
// refuses dropping one in a space the caller does not administer, and
// keeps or drops the rest.
func TestUnitReleasable(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	bob := kernel.NewULID()
	rule := func(space string) binding.Listed {
		return binding.Listed{Binding: binding.Binding{
			ID:        kernel.NewULID(),
			ActorKind: binding.ActorUser,
			ActorID:   bob,
			Origin:    binding.OriginAccessRule,
			RoleID:    f.st.roles["space-reader"],
			SpaceID:   f.st.spaces[space],
		}, Space: space}
	}
	team := rule("team")
	other := team
	other.Origin = binding.OriginBinding
	f.st.all = []binding.Listed{rule("top"), team, other}
	if err := f.svc.Releasable(
		ctx,
		f.p,
		binding.ActorUser,
		bob,
		"/accessRules",
		[]binding.Binding{team.Binding},
	); err != nil {
		t.Errorf("keeping team's = %v", err)
	}
	if got := pointers(
		t,
		f.svc.Releasable(ctx, f.p, binding.ActorUser, bob, "/accessRules", nil),
	); !slices.Equal(
		got,
		[]string{"/accessRules"},
	) {
		t.Errorf("dropping team's = %v", got)
	}
	f.authz.err = errDown
	if err := f.svc.Releasable(
		ctx,
		f.p,
		binding.ActorUser,
		bob,
		"/accessRules",
		nil,
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("authorizer down = %v", err)
	}
	f.st.fail["ActorBindings"] = true
	if err := f.svc.Releasable(
		ctx,
		f.p,
		binding.ActorUser,
		bob,
		"/accessRules",
		nil,
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("store down = %v", err)
	}
}

// TestUnitDuplicate checks that a duplicate names its index and is
// ErrDuplicate.
func TestUnitDuplicate(t *testing.T) {
	err := error(&binding.Duplicate{Index: 3})
	if !errors.Is(err, binding.ErrDuplicate) ||
		err.Error() != "binding 3: "+binding.ErrDuplicate.Error() {
		t.Errorf("duplicate = %v", err)
	}
}
