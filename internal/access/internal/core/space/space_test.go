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
	acct   kernel.AccountID
	spaces map[string]space.Space
	err    error
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

func (f *tree) MoveSpace(
	ctx context.Context,
	acct kernel.AccountID,
	slug, parentSlug string,
	check func(context.Context, space.Space, space.Space, accessapi.SpaceMove) error,
) error {
	moved, err := f.Space(ctx, acct, slug)
	if err != nil {
		return err
	}
	parent, err := f.Space(ctx, acct, parentSlug)
	if err != nil {
		return err
	}
	if err := check(
		ctx,
		moved,
		parent,
		accessapi.SpaceMove{Account: acct, Space: moved.ID},
	); err != nil {
		return err
	}
	moved.ParentID = parent.ID
	f.spaces[slug] = moved
	return nil
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

func TestUnitSpaceService(t *testing.T) {
	ctx := t.Context()
	f, top, legacy := newTree(t)
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	svc := space.NewService(f, func() time.Time { return at })

	prod, err := svc.Create(ctx, f.acct, top.Slug, space.Space{
		Name: "Production", Labels: []string{"env"}, InheritEntities: true,
	})
	if err != nil || prod.ParentID != top.ID || prod.AccountID != f.acct ||
		prod.Slug != space.SlugOf("Production", prod.ID) || !prod.CreatedAt.Equal(at) ||
		!prod.InheritEntities {
		t.Fatalf("Create = %+v, %v", prod, err)
	}
	if _, err := svc.Create(ctx, f.acct, top.Slug, space.Space{}); !errors.Is(err, space.ErrName) {
		t.Errorf("unnamed Create = %v, want ErrName", err)
	}
	if _, err := svc.Create(
		ctx,
		f.acct,
		"nope",
		space.Space{Name: "x"},
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("Create below a missing parent = %v, want not found", err)
	}
	if got, err := svc.Spaces(ctx, f.acct); err != nil || len(got) != 3 {
		t.Errorf("Spaces = %v, %v", got, err)
	}

	if err := svc.Move(ctx, f.acct, prod.Slug, legacy.Slug); err != nil {
		t.Errorf("Move = %v", err)
	}
	refusal(t, svc.Move(ctx, f.acct, top.Slug, legacy.Slug), "top space acme cannot move")
	refusal(t, svc.Delete(ctx, f.acct, top.Slug), "top space acme cannot be deleted")
	refusal(t, svc.Delete(ctx, f.acct, legacy.Slug), "legacy space cannot be deleted")

	g := &guard{used: map[kernel.ULID]bool{prod.ID: true}, lost: []accessapi.LostReach{
		{Stack: "web", Entity: "worker pool private"}, {Stack: "db", Entity: "context shared"},
	}}
	svc.Guard(g)
	err = svc.Move(ctx, f.acct, prod.Slug, top.Slug)
	refusal(t, err, "stack web would no longer reach worker pool private")
	refusal(t, err, "stack db would no longer reach context shared")
	refusal(t, svc.Delete(ctx, f.acct, prod.Slug), "space "+prod.Slug+" still holds entities")

	g.err = errors.New("stacks unreachable")
	if err := svc.Move(ctx, f.acct, prod.Slug, top.Slug); !errors.Is(err, g.err) {
		t.Errorf("Move with a failing guard = %v", err)
	}
	if err := svc.Delete(ctx, f.acct, prod.Slug); !errors.Is(err, g.err) {
		t.Errorf("Delete with a failing guard = %v", err)
	}

	g.err, g.lost, g.used = nil, nil, nil
	if err := svc.Move(ctx, f.acct, prod.Slug, top.Slug); err != nil {
		t.Errorf("Move a guard allows = %v", err)
	}
	if err := svc.Delete(ctx, f.acct, prod.Slug); err != nil {
		t.Errorf("Delete a guard allows = %v", err)
	}

	f.err = errors.New("store down")
	if _, err := svc.Create(ctx, f.acct, top.Slug, space.Space{Name: "x"}); !errors.Is(err, f.err) {
		t.Errorf("Create on a failing store = %v", err)
	}
}
