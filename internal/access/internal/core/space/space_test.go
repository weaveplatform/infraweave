package space_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

func TestUnitSlugOf(t *testing.T) {
	id := kernel.NewULID()
	for name, want := range map[string]string{
		"Production":       "production-" + id.String(),
		"us-east-1":        "us-east-1-" + id.String(),
		"  Team  A / B  ":  "team-a-b-" + id.String(),
		"Ünïcode Späce!":   "n-code-sp-ce-" + id.String(),
		"--":               id.String(),
		"already-hyphened": "already-hyphened-" + id.String(),
	} {
		if got := space.SlugOf(name, id); got != want {
			t.Errorf("SlugOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestUnitSeed(t *testing.T) {
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	a := account.Account{ID: kernel.NewAccountID(), Slug: "acme", Name: "Acme", CreatedAt: at}
	top, legacy := space.Seed(a)
	if !top.Top() || top.ID != a.ID.ULID || top.Slug != "acme" || top.Name != "Acme" ||
		top.AccountID != a.ID || !top.CreatedAt.Equal(at) {
		t.Errorf("top = %+v", top)
	}
	if legacy.Top() || legacy.ParentID != top.ID || legacy.Slug != space.LegacySlug ||
		legacy.ID == top.ID || legacy.AccountID != a.ID || legacy.InheritEntities {
		t.Errorf("legacy = %+v", legacy)
	}
}

// tree is an in-memory Store of one account's spaces.
type tree struct {
	acct    kernel.AccountID
	spaces  map[string]space.Space
	setting bool
	err     error
}

func newTree(t *testing.T) (*tree, space.Space, space.Space) {
	t.Helper()
	a := account.Account{ID: kernel.NewAccountID(), Slug: "acme", Name: "Acme"}
	top, legacy := space.Seed(a)
	return &tree{acct: a.ID, spaces: map[string]space.Space{top.Slug: top, legacy.Slug: legacy}},
		top, legacy
}

func (f *tree) Spaces(_ context.Context, acct kernel.AccountID) ([]space.Space, error) {
	if acct != f.acct {
		return nil, port.ErrNotFound
	}
	var out []space.Space
	for _, s := range f.spaces {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b space.Space) int { return strings.Compare(a.Slug, b.Slug) })
	return out, f.err
}

func (f *tree) Space(_ context.Context, acct kernel.AccountID, slug string) (space.Space, error) {
	s, ok := f.spaces[slug]
	if !ok || acct != f.acct {
		return space.Space{}, port.ErrNotFound
	}
	return s, nil
}

func (f *tree) CreateSpace(_ context.Context, s space.Space) error {
	if f.err != nil {
		return f.err
	}
	f.spaces[s.Slug] = s
	return nil
}

func (f *tree) UpdateSpace(
	ctx context.Context,
	acct kernel.AccountID,
	slug, parentSlug string,
	edit func(context.Context, space.Space, space.Space, *accessapi.SpaceMove) (space.Space, error),
) (space.Space, error) {
	cur, err := f.Space(ctx, acct, slug)
	if err != nil {
		return space.Space{}, err
	}
	var parent space.Space
	if parentSlug != "" {
		if parent, err = f.Space(ctx, acct, parentSlug); err != nil {
			return space.Space{}, err
		}
	}
	var m *accessapi.SpaceMove
	if parentSlug != "" && parent.ID != cur.ParentID {
		m = &accessapi.SpaceMove{Account: acct, Space: cur.ID}
	}
	out, err := edit(ctx, cur, parent, m)
	if err != nil {
		return space.Space{}, err
	}
	out.Version = cur.Version + 1
	f.spaces[slug] = out
	return out, nil
}

func (f *tree) HoldSpace(_ context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if f.err != nil {
		return f.err
	}
	for _, s := range f.spaces {
		if s.ID == id && acct == f.acct {
			return nil
		}
	}
	return port.ErrNotFound
}

func (f *tree) DeleteSpace(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	check func(context.Context, space.Space) error,
) error {
	s, err := f.Space(ctx, acct, slug)
	if err != nil {
		return err
	}
	if err := check(ctx, s); err != nil {
		return err
	}
	delete(f.spaces, slug)
	return nil
}

func (f *tree) NonAdminCreation(context.Context, kernel.AccountID) (bool, error) {
	return f.setting, f.err
}

func (f *tree) ToggleNonAdminCreation(context.Context, kernel.AccountID) (bool, error) {
	f.setting = !f.setting
	return f.setting, f.err
}

// errSearched marks the store's search, which Service hands on as it is.
var errSearched = errors.New("searched")

func (f *tree) Search(
	context.Context,
	kernel.AccountID,
	search.Input,
	[]kernel.ULID,
) (search.Result[space.Space], error) {
	return search.Result[space.Space]{}, errSearched
}

func (f *tree) Suggest(
	context.Context,
	kernel.AccountID,
	search.SuggestionsInput,
	[]kernel.ULID,
) (search.Suggestions, error) {
	return search.Suggestions{}, errSearched
}

// admins is a space.Authorizer granting SPACE_ADMIN on the spaces it
// holds; err fails every decision.
type admins struct {
	on  map[kernel.ULID]bool
	err error
}

func (a *admins) Authorize(
	_ context.Context,
	_ kernel.Principal,
	act accessapi.Action,
	sp kernel.ULID,
) (bool, error) {
	return act == accessapi.ActionSpaceAdmin && a.on[sp], a.err
}

// guard is a SpaceGuard that holds used and strands lost.
type guard struct {
	used map[kernel.ULID]bool
	lost []accessapi.LostReach
	err  error
}

func (g *guard) SpaceInUse(_ context.Context, _ kernel.AccountID, s kernel.ULID) (bool, error) {
	return g.used[s], g.err
}

func (g *guard) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return g.lost, g.err
}

func refusal(t *testing.T, err error, want string) {
	t.Helper()
	r, ok := errors.AsType[*space.Refusal](err)
	if !ok || !strings.Contains(r.Error(), want) {
		t.Errorf("err = %v, want a refusal naming %q", err, want)
	}
}

func denied(t *testing.T, err error, want string) {
	t.Helper()
	d, ok := errors.AsType[*space.Denied](err)
	if !ok || d.Action != accessapi.ActionSpaceAdmin || !strings.Contains(d.Error(), want) {
		t.Errorf("err = %v, want SPACE_ADMIN denied naming %q", err, want)
	}
}

// world is a service over a tree whose caller is an account admin until
// a test takes the top space's binding away.
type world struct {
	f           *tree
	top, legacy space.Space
	authz       *admins
	svc         *space.Service
	p           kernel.Principal
	at          time.Time
	// dropped are the accounts whose snapshot each change dropped.
	dropped *[]kernel.AccountID
}

func newWorld(t *testing.T) world {
	t.Helper()
	f, top, legacy := newTree(t)
	authz := &admins{on: map[kernel.ULID]bool{top.ID: true}}
	dropped := &[]kernel.AccountID{}
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	return world{
		f:      f,
		top:    top,
		legacy: legacy,
		authz:  authz,
		at:     at,
		svc: space.NewService(space.Deps{
			Store: f, Authz: authz, Now: func() time.Time { return at },
			Drop: func(a kernel.AccountID) { *dropped = append(*dropped, a) },
		}),
		dropped: dropped,
		p: kernel.Principal{
			Kind:      kernel.PrincipalUser,
			ActorID:   kernel.NewULID(),
			AccountID: f.acct,
		},
	}
}

// move moves slug below parent, keeping its other fields.
func (w world) move(t *testing.T, slug, parent string) error {
	t.Helper()
	cur, err := w.svc.Space(t.Context(), w.f.acct, slug)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.Update(t.Context(), w.p, slug, parent, kernel.Expect{}, cur)
	return err
}

func (w world) del(t *testing.T, slug string) error {
	t.Helper()
	_, err := w.svc.Delete(t.Context(), w.p, slug, kernel.Expect{})
	return err
}

func TestUnitSpaceService(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	top, legacy := w.top, w.legacy
	prod, err := w.svc.Create(ctx, w.p, top.Slug, space.Space{
		Name: "Production", Labels: []string{"env"}, InheritEntities: true,
	})
	if err != nil || prod.ParentID != top.ID || prod.AccountID != w.f.acct ||
		prod.Slug != space.SlugOf("Production", prod.ID) || !prod.CreatedAt.Equal(w.at) ||
		!prod.InheritEntities {
		t.Fatalf("Create = %+v, %v", prod, err)
	}
	if _, err := w.svc.Create(ctx, w.p, top.Slug, space.Space{}); !errors.Is(err, space.ErrName) {
		t.Errorf("unnamed Create = %v, want ErrName", err)
	}
	if _, err := w.svc.Create(
		ctx,
		w.p,
		"nope",
		space.Space{Name: "x"},
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("Create below a missing parent = %v, want not found", err)
	}
	if got, err := w.svc.Spaces(ctx, w.f.acct); err != nil || len(got) != 3 {
		t.Errorf("Spaces = %v, %v", got, err)
	}
	if _, err := w.svc.Search(ctx, w.f.acct, search.Input{}, nil); !errors.Is(err, errSearched) {
		t.Errorf("Search = %v, want the store's", err)
	}
	if _, err := w.svc.Suggest(
		ctx,
		w.f.acct,
		search.SuggestionsInput{},
		nil,
	); !errors.Is(
		err,
		errSearched,
	) {
		t.Errorf("Suggest = %v, want the store's", err)
	}

	renamed, err := w.svc.Update(ctx, w.p, prod.Slug, legacy.Slug, kernel.Expect{}, space.Space{
		Name: "Prod", Description: "live", Labels: []string{"tier"},
	})
	if err != nil || renamed.Name != "Prod" || renamed.Description != "live" ||
		renamed.ParentID != legacy.ID || renamed.InheritEntities || renamed.Slug != prod.Slug {
		t.Errorf("Update = %+v, %v", renamed, err)
	}
	if _, err := w.svc.Update(
		ctx,
		w.p,
		prod.Slug,
		top.Slug,
		kernel.Expect{},
		space.Space{},
	); !errors.Is(
		err,
		space.ErrName,
	) {
		t.Errorf("unnamed Update = %v, want ErrName", err)
	}
	refusal(t, w.move(t, top.Slug, legacy.Slug), "top space acme cannot move")
	_, err = w.svc.Update(
		ctx,
		w.p,
		top.Slug,
		"",
		kernel.Expect{},
		space.Space{Name: "Acme", InheritEntities: true},
	)
	refusal(t, err, "top space acme has no parent to inherit from")
	if got, err := w.svc.Update(
		ctx,
		w.p,
		top.Slug,
		"",
		kernel.Expect{},
		space.Space{Name: "Acme Corp"},
	); err != nil ||
		got.Name != "Acme Corp" {
		t.Errorf("rename the top space = %+v, %v", got, err)
	}
	_, err = w.svc.Update(ctx, w.p, prod.Slug, "", kernel.Expect{}, space.Space{Name: "Prod"})
	refusal(t, err, "space "+prod.Slug+" needs a parent space")
	refusal(t, w.del(t, top.Slug), "top space acme cannot be deleted")
	refusal(t, w.del(t, legacy.Slug), "legacy space cannot be deleted")

	g := &guard{used: map[kernel.ULID]bool{prod.ID: true}, lost: []accessapi.LostReach{
		{Stack: "web", Entity: "worker pool private"}, {Stack: "db", Entity: "context shared"},
	}}
	w.svc.Guard(g)
	err = w.move(t, prod.Slug, top.Slug)
	refusal(t, err, "stack web would no longer reach worker pool private")
	refusal(t, err, "stack db would no longer reach context shared")
	refusal(t, w.del(t, prod.Slug), "space "+prod.Slug+" still holds entities")
	if _, err := w.svc.Update(
		ctx,
		w.p,
		prod.Slug,
		legacy.Slug,
		kernel.Expect{},
		space.Space{Name: "P"},
	); err != nil {
		t.Errorf("a rename, which no guard sees = %v", err)
	}

	g.err = errors.New("stacks unreachable")
	if err := w.move(t, prod.Slug, top.Slug); !errors.Is(err, g.err) {
		t.Errorf("Move with a failing guard = %v", err)
	}
	if err := w.del(t, prod.Slug); !errors.Is(err, g.err) {
		t.Errorf("Delete with a failing guard = %v", err)
	}

	g.err, g.lost, g.used = nil, nil, nil
	if err := w.move(t, prod.Slug, top.Slug); err != nil {
		t.Errorf("Move a guard allows = %v", err)
	}
	if gone, err := w.svc.Delete(
		ctx,
		w.p,
		prod.Slug,
		kernel.Expect{},
	); err != nil ||
		gone.ID != prod.ID {
		t.Errorf("Delete a guard allows = %+v, %v", gone, err)
	}
	// Each change that succeeded dropped the snapshot, and no refusal did:
	// the create, the update, the top space's rename, the rename a guard
	// does not see, the move and the delete.
	if n := len(*w.dropped); n != 6 || (*w.dropped)[0] != w.f.acct {
		t.Errorf("dropped %d snapshots: %v", n, *w.dropped)
	}

	w.f.err = errors.New("store down")
	if _, err := w.svc.Create(
		ctx,
		w.p,
		top.Slug,
		space.Space{Name: "x"},
	); !errors.Is(
		err,
		w.f.err,
	) {
		t.Errorf("Create on a failing store = %v", err)
	}
}

// TestUnitSpaceHold checks that a hold names a missing space by
// accessapi.ErrNoSpace and passes a store failure through as it is.
func TestUnitSpaceHold(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	if err := w.svc.Hold(ctx, w.f.acct, w.legacy.ID); err != nil {
		t.Errorf("Hold of legacy = %v", err)
	}
	for name, err := range map[string]error{
		"a missing space":       w.svc.Hold(ctx, w.f.acct, kernel.NewULID()),
		"another account's one": w.svc.Hold(ctx, kernel.NewAccountID(), w.legacy.ID),
	} {
		if !errors.Is(err, accessapi.ErrNoSpace) {
			t.Errorf("Hold of %s = %v, want ErrNoSpace", name, err)
		}
	}
	w.f.err = errors.New("unavailable")
	if err := w.svc.Hold(ctx, w.f.acct, w.legacy.ID); !errors.Is(err, w.f.err) ||
		errors.Is(err, accessapi.ErrNoSpace) {
		t.Errorf("Hold on a failing store = %v", err)
	}
}

// TestUnitSpaceAdminBelowTop checks what an admin of a space below the top
// space may do with the setting off and on.
func TestUnitSpaceAdminBelowTop(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	team, err := w.svc.Create(ctx, w.p, w.top.Slug, space.Space{Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := w.svc.Create(ctx, w.p, w.top.Slug, space.Space{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	w.authz.on = map[kernel.ULID]bool{team.ID: true}

	got, err := w.svc.Update(ctx, w.p, team.Slug, w.top.Slug, kernel.Expect{}, space.Space{
		Name: "Team A", Description: "ours", Labels: []string{"x"},
	})
	if err != nil || got.Name != "Team A" || got.Description != "ours" {
		t.Errorf("rename with the setting off = %+v, %v", got, err)
	}
	_, err = w.svc.Create(ctx, w.p, team.Slug, space.Space{Name: "Sub", InheritEntities: true})
	denied(t, err, "Only account admins create spaces while allowNonAdminSpaceCreation is off")
	denied(t, w.move(t, team.Slug, w.legacy.Slug), "Only account admins move spaces")
	_, err = w.svc.Update(
		ctx,
		w.p,
		team.Slug,
		w.top.Slug,
		kernel.Expect{},
		space.Space{Name: "T", InheritEntities: true},
	)
	denied(t, err, "change their inheritance")
	denied(t, w.del(t, team.Slug), "Only account admins delete spaces")
	_, err = w.svc.Update(
		ctx,
		w.p,
		other.Slug,
		w.top.Slug,
		kernel.Expect{},
		space.Space{Name: "Mine"},
	)
	denied(t, err, "You may not perform SPACE_ADMIN on space "+other.Slug)

	if on, err := w.svc.ToggleNonAdminCreation(ctx, w.p); err != nil || !on {
		t.Fatalf("ToggleNonAdminCreation = %t, %v", on, err)
	}
	if on, err := w.svc.NonAdminCreation(ctx, w.f.acct); err != nil || !on {
		t.Errorf("NonAdminCreation = %t, %v", on, err)
	}
	_, err = w.svc.Create(ctx, w.p, team.Slug, space.Space{Name: "Sub"})
	refusal(t, err, "a space an admin below the top space creates inherits its parent's entities")
	sub, err := w.svc.Create(ctx, w.p, team.Slug, space.Space{Name: "Sub", InheritEntities: true})
	if err != nil || sub.ParentID != team.ID || !sub.InheritEntities {
		t.Fatalf("create below an administered space = %+v, %v", sub, err)
	}
	w.authz.on[sub.ID] = true
	_, err = w.svc.Create(ctx, w.p, other.Slug, space.Space{Name: "No", InheritEntities: true})
	denied(t, err, "on space "+other.Slug)
	denied(t, w.move(t, sub.Slug, other.Slug), "on space "+other.Slug)
	_, err = w.svc.Update(ctx, w.p, sub.Slug, team.Slug, kernel.Expect{}, space.Space{Name: "Sub"})
	denied(t, err, "Only account admins stop space "+sub.Slug+" inheriting")
	if got, err := w.svc.Update(ctx, w.p, team.Slug, w.top.Slug,
		kernel.Expect{},
		space.Space{Name: "Team", InheritEntities: true}); err != nil || !got.InheritEntities {
		t.Errorf("start inheriting = %+v, %v", got, err)
	}
	second, err := w.svc.Create(
		ctx,
		w.p,
		team.Slug,
		space.Space{Name: "Second", InheritEntities: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	w.authz.on[second.ID] = true
	if err := w.move(t, sub.Slug, second.Slug); err != nil {
		t.Errorf("move between administered spaces = %v", err)
	}
	if gone, err := w.svc.Delete(
		ctx,
		w.p,
		sub.Slug,
		kernel.Expect{},
	); err != nil ||
		gone.ID != sub.ID {
		t.Errorf("delete an administered space = %+v, %v", gone, err)
	}

	w.authz.err = errors.New("snapshot unreadable")
	for name, err := range map[string]error{
		"create": func() error { _, err := w.svc.Create(ctx, w.p, team.Slug, space.Space{Name: "x"}); return err }(),
		"update": w.move(t, team.Slug, w.top.Slug),
		"delete": w.del(t, second.Slug),
	} {
		if !errors.Is(err, w.authz.err) {
			t.Errorf("%s with a failing authorizer = %v", name, err)
		}
	}
	w.authz.err = nil
	w.f.err = errors.New("store down")
	if err := w.del(t, second.Slug); !errors.Is(err, w.f.err) {
		t.Errorf("delete when the setting cannot be read = %v", err)
	}
}

// TestUnitSpaceVersions checks that an update or delete whose expect does
// not accept the space's version is kernel.ErrStale and changes nothing,
// and that the version read, or any, is accepted.
func TestUnitSpaceVersions(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	dev, err := w.svc.Create(ctx, w.p, w.top.Slug, space.Space{Name: "Dev"})
	if err != nil {
		t.Fatal(err)
	}
	dev.Version = 1
	w.f.spaces[dev.Slug] = dev
	stale := kernel.ExpectVersions(7)

	if _, err := w.svc.Update(ctx, w.p, dev.Slug, w.top.Slug, stale,
		space.Space{Name: "Changed"}); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale update = %v, want ErrStale", err)
	}
	if _, err := w.svc.Delete(ctx, w.p, dev.Slug, stale); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale delete = %v, want ErrStale", err)
	}
	if got := w.f.spaces[dev.Slug]; got.Name != "Dev" || got.Version != 1 {
		t.Errorf("after the stale writes = %+v", got)
	}
	// The drop follows the create alone.
	if n := len(*w.dropped); n != 1 {
		t.Errorf("dropped %d snapshots", n)
	}

	got, err := w.svc.Update(ctx, w.p, dev.Slug, w.top.Slug, kernel.ExpectVersions(1),
		space.Space{Name: "Development"})
	if err != nil || got.Name != "Development" || got.Version != 2 {
		t.Errorf("an update naming the version = %+v, %v", got, err)
	}
	if _, err := w.svc.Delete(ctx, w.p, dev.Slug, kernel.ExpectAny()); err != nil {
		t.Errorf("a delete of any version = %v", err)
	}
}

// TestUnitSpaceLabels checks that a create and an update keep labels in
// the order given and refuse each one breaking the label rule at its
// pointer, a repeat included, writing nothing.
func TestUnitSpaceLabels(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	dev, err := w.svc.Create(ctx, w.p, w.top.Slug, space.Space{
		Name: "Dev", Labels: []string{"zeta", "alpha", "Alpha"},
	})
	if err != nil || !slices.Equal(dev.Labels, []string{"zeta", "alpha", "Alpha"}) {
		t.Fatalf("Create = %+v, %v", dev, err)
	}
	bad := []string{"ok", "", " edge", "ok", "tab\there"}
	want := []kernel.FieldError{
		{Pointer: "/labels/1", Reason: kernel.ReasonMinLength},
		{Pointer: "/labels/2", Reason: kernel.ReasonPattern},
		{Pointer: "/labels/3", Reason: kernel.ReasonDuplicate},
		{Pointer: "/labels/4", Reason: kernel.ReasonPattern},
	}
	for name, call := range map[string]func() error{
		"create": func() error {
			_, err := w.svc.Create(ctx, w.p, w.top.Slug, space.Space{Name: "X", Labels: bad})
			return err
		},
		"update": func() error {
			_, err := w.svc.Update(ctx, w.p, dev.Slug, w.top.Slug,
				kernel.Expect{},
				space.Space{Name: "Dev", Labels: bad})
			return err
		},
	} {
		inv, ok := errors.AsType[*kernel.Invalid](call())
		if !ok || len(inv.Errors) != len(want) {
			t.Errorf("%s with bad labels = %v", name, inv)
			continue
		}
		for i, e := range inv.Errors {
			if e.Pointer != want[i].Pointer || e.Reason != want[i].Reason || e.Message == "" {
				t.Errorf("%s refusal %d = %+v, want %+v", name, i, e, want[i])
			}
		}
	}
	if got := w.f.spaces[dev.Slug]; !slices.Equal(got.Labels, dev.Labels) || len(w.f.spaces) != 3 {
		t.Errorf("a refused write changed the tree: %+v, %d spaces", got, len(w.f.spaces))
	}
	if _, err := w.svc.Update(ctx, w.p, dev.Slug, w.top.Slug,
		kernel.Expect{},
		space.Space{Name: "Dev", Labels: make([]string, kernel.MaxLabels+1)}); err == nil {
		t.Error("an update with too many labels succeeded")
	}
}

// TestUnitSpaceFind finds a space's ID by slug, names a missing one by
// accessapi.ErrNoSpace, and names every space by its slug.
func TestUnitSpaceFind(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	if id, err := w.svc.Find(ctx, w.f.acct, space.LegacySlug); err != nil || id != w.legacy.ID {
		t.Errorf("Find(legacy) = %v, %v", id, err)
	}
	if _, err := w.svc.Find(ctx, w.f.acct, "nope"); !errors.Is(err, accessapi.ErrNoSpace) {
		t.Errorf("Find(nope) = %v, want ErrNoSpace", err)
	}
	slugs, err := w.svc.Slugs(ctx, w.f.acct)
	if err != nil || len(slugs) != 2 || slugs[w.top.ID] != w.top.Slug ||
		slugs[w.legacy.ID] != space.LegacySlug {
		t.Errorf("Slugs = %v, %v", slugs, err)
	}
	w.f.err = errors.New("store down")
	if _, err := w.svc.Slugs(ctx, w.f.acct); !errors.Is(err, w.f.err) {
		t.Errorf("Slugs on a failing store = %v", err)
	}
}
