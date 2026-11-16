package http

import (
	"context"
	"errors"
	"math"
	"net/http"
	"reflect"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// fake answers each call with its fields and records the principal,
// address and argument it was given.
type fake struct {
	result  search.Result[core.Workspace]
	suggest search.Suggestions
	one     core.Workspace
	many    []core.Workspace
	err     error
	who     kernel.Principal
	addr    string
	arg     any
}

func (f *fake) SearchWorkspaces(_ context.Context, p kernel.Principal, in search.Input) (search.Result[core.Workspace], error) {
	f.who, f.arg = p, in
	return f.result, f.err
}

func (f *fake) SuggestWorkspaces(_ context.Context, p kernel.Principal, in search.SuggestionsInput) (search.Suggestions, error) {
	f.who, f.arg = p, in
	return f.suggest, f.err
}

func (f *fake) UnlockWorkspace(_ context.Context, p kernel.Principal, addr, raw string) (core.Workspace, error) {
	f.who, f.addr, f.arg = p, addr, raw
	return f.one, f.err
}

func (f *fake) UnlockStack(_ context.Context, p kernel.Principal, addr, slug string) ([]core.Workspace, error) {
	f.who, f.addr, f.arg = p, addr, slug
	return f.many, f.err
}

var (
	created = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	held    = core.Workspace{
		ID: kernel.NewULID(), Name: "", CreatedAt: created, Locked: true, TriggeredBy: "alice",
	}
	free = core.Workspace{ID: kernel.NewULID(), Name: "blue", CreatedAt: created}
)

// TestUnitView checks a workspace as the API answers it: the default
// workspace named default, its creation in Unix seconds, and lockedBy
// only while it is held.
func TestUnitView(t *testing.T) {
	alice := "alice"
	for _, tc := range []struct {
		w    core.Workspace
		want api.TofuWorkspace
	}{
		{held, api.TofuWorkspace{
			Id: held.ID.String(), Name: "default", CreatedAt: int32(created.Unix()), IsLocked: true,
			LockedBy: &alice,
		}},
		{free, api.TofuWorkspace{Id: free.ID.String(), Name: "blue", CreatedAt: int32(created.Unix())}},
	} {
		if got := view(tc.w); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("view(%+v) = %+v, want %+v", tc.w, got, tc.want)
		}
	}
	for in, want := range map[int64]int32{-1: 0, 7: 7, math.MaxInt32 + 1: math.MaxInt32} {
		if got := unix(in); got != want {
			t.Errorf("unix(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestUnitHandlers checks that each operation hands the core the caller,
// an unlock the caller's address too, and its argument, and answers what
// the core answers.
func TestUnitHandlers(t *testing.T) {
	p := kernel.Principal{Kind: kernel.PrincipalUser, AccountID: kernel.NewAccountID(), Login: "dave"}
	ctx := kernel.WithPrincipal(accessapi.WithClientAddr(t.Context(), "192.0.2.1"), p)
	f := &fake{
		result: search.Result[core.Workspace]{
			Edges: []search.Edge[core.Workspace]{{Cursor: "c1", Node: held}, {Cursor: "c2", Node: free}},
			Start: "c1", End: "c2", HasNext: true,
		},
		suggest: search.Suggestions{Count: 2},
		one:     free,
		many:    []core.Workspace{free},
	}
	s := New(f)

	first := int32(2)
	res, err := s.SearchTofuWorkspaces(ctx, api.SearchTofuWorkspacesRequestObject{
		Body: &api.SearchInput{First: &first},
	})
	out, _ := res.(api.SearchTofuWorkspaces200JSONResponse)
	if err != nil || len(out.Body.Edges) != 2 || out.Body.Edges[1].Cursor != "c2" ||
		out.Body.Edges[0].Node.Name != "default" || !out.Body.PageInfo.HasNextPage ||
		f.who.Login != "dave" || *f.arg.(search.Input).First != 2 {
		t.Errorf("SearchTofuWorkspaces = %+v, %v", res, err)
	}

	sres, err := s.SearchTofuWorkspacesSuggestions(ctx, api.SearchTofuWorkspacesSuggestionsRequestObject{
		Body: &api.SearchSuggestionsInput{Fields: &[]string{"name"}},
	})
	sout, _ := sres.(api.SearchTofuWorkspacesSuggestions200JSONResponse)
	if err != nil || sout.Body.FilteredCount != 2 ||
		!reflect.DeepEqual(f.arg.(search.SuggestionsInput).Fields, []string{"name"}) {
		t.Errorf("SearchTofuWorkspacesSuggestions = %+v, %v", sres, err)
	}

	ures, err := s.TofuWorkspaceUnlock(ctx, api.TofuWorkspaceUnlockRequestObject{TofuWorkspaceId: "w1"})
	uout, _ := ures.(api.TofuWorkspaceUnlock200JSONResponse)
	if err != nil || uout.Body.Id != free.ID.String() || f.arg != "w1" || f.addr != "192.0.2.1" {
		t.Errorf("TofuWorkspaceUnlock = %+v, %v", ures, err)
	}

	ares, err := s.TofuWorkspaceUnlockAll(ctx, api.TofuWorkspaceUnlockAllRequestObject{StackId: "app"})
	aout, _ := ares.(api.TofuWorkspaceUnlockAll200JSONResponse)
	if err != nil || len(aout.Body.UnlockedWorkspaces) != 1 ||
		aout.Body.UnlockedWorkspaces[0].Name != "blue" || f.arg != "app" || f.addr != "192.0.2.1" {
		t.Errorf("TofuWorkspaceUnlockAll = %+v, %v", ares, err)
	}
	f.many = []core.Workspace{}
	ares, err = s.TofuWorkspaceUnlockAll(ctx, api.TofuWorkspaceUnlockAllRequestObject{StackId: "app"})
	if aout, _ := ares.(api.TofuWorkspaceUnlockAll200JSONResponse); err != nil ||
		aout.Body.UnlockedWorkspaces == nil {
		t.Errorf("a stack with no lock = %+v, %v", ares, err)
	}
}

// TestUnitRefusals checks that a call without a principal is refused, a
// workspace or stack the account lacks answers 404, and any other error
// passes as it is.
func TestUnitRefusals(t *testing.T) {
	down := errors.New("down")
	s := New(&fake{err: down})
	bare := t.Context()
	ctx := kernel.WithPrincipal(bare, kernel.Principal{AccountID: kernel.NewAccountID()})
	calls := map[string]func(context.Context) error{
		"search": func(ctx context.Context) error {
			_, err := s.SearchTofuWorkspaces(ctx, api.SearchTofuWorkspacesRequestObject{
				Body: &api.SearchInput{},
			})
			return err
		},
		"suggest": func(ctx context.Context) error {
			_, err := s.SearchTofuWorkspacesSuggestions(ctx,
				api.SearchTofuWorkspacesSuggestionsRequestObject{Body: &api.SearchSuggestionsInput{}})
			return err
		},
		"unlock": func(ctx context.Context) error {
			_, err := s.TofuWorkspaceUnlock(ctx, api.TofuWorkspaceUnlockRequestObject{})
			return err
		},
		"unlock all": func(ctx context.Context) error {
			_, err := s.TofuWorkspaceUnlockAll(ctx, api.TofuWorkspaceUnlockAllRequestObject{})
			return err
		},
	}
	for name, call := range calls {
		if p, ok := errors.AsType[*problem.Error](call(bare)); !ok ||
			p.Problem.Status != http.StatusUnauthorized {
			t.Errorf("%s without a principal = %v", name, call(bare))
		}
		if err := call(ctx); !errors.Is(err, down) {
			t.Errorf("%s with the core down = %v", name, err)
		}
	}
	for _, err := range []error{core.ErrNotFound, stacksapi.ErrNotFound} {
		s := New(&fake{err: err})
		_, uerr := s.TofuWorkspaceUnlock(ctx, api.TofuWorkspaceUnlockRequestObject{TofuWorkspaceId: "w"})
		_, aerr := s.TofuWorkspaceUnlockAll(ctx, api.TofuWorkspaceUnlockAllRequestObject{StackId: "s"})
		for _, got := range []error{uerr, aerr} {
			if p, ok := errors.AsType[*problem.Error](got); !ok ||
				p.Problem.Status != http.StatusNotFound {
				t.Errorf("%v = %v, want 404", err, got)
			}
		}
	}
}
