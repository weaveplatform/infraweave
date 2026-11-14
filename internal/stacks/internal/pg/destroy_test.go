package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// destroyer is a StackDestroyer that records the stacks it is asked to
// destroy and fails with err.
type destroyer struct {
	asked []kernel.ULID
	err   error
}

func (d *destroyer) Destroy(_ context.Context, _ kernel.Principal, id kernel.ULID) error {
	if d.err != nil {
		return d.err
	}
	d.asked = append(d.asked, id)
	return nil
}

// TestAccServiceDestroy checks a delete with destroy: refused while no
// destroyer is bound and when the destroyer refuses, leaving the stack as
// it was; otherwise the stack stays, deleting, at its version, and
// answers as it is to another such delete without a second run, while a
// delete without destroy is refused with ErrDeleting. Destroyed keeps a
// stack whose run did not finish, no longer deleting, deletes one whose
// run finished, with each guard's rows, and leaves a stack that is not
// deleting alone. A protected stack refuses both forms.
func TestAccServiceDestroy(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	st, err := f.svc.Create(ctx, f.admin, "", false, input("Infra"))
	if err != nil {
		t.Fatal(err)
	}
	deleting := func(want bool) {
		t.Helper()
		got, err := f.svc.Stack(ctx, f.acct, "infra")
		if err != nil || got.Deleting != want || got.Version != st.Version {
			t.Errorf("the stack reads %v at version %d, %v; want deleting %v at %d",
				got.Deleting, got.Version, err, want, st.Version)
		}
	}

	_, err = f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{}, true)
	if u, ok := errors.AsType[*stacksapi.Undestroyable](err); !ok ||
		u.Detail != "This server creates no destroy runs." {
		t.Errorf("a destroy with no destroyer bound = %v", err)
	}
	d := &destroyer{err: &stacksapi.Undestroyable{Detail: "The stack is disabled."}}
	f.svc.BindDestroyer(d)
	if _, err := f.svc.Delete(
		ctx,
		f.admin,
		"infra",
		kernel.Expect{},
		true,
	); !errors.Is(
		err,
		d.err,
	) {
		t.Errorf("a destroy the destroyer refuses = %v", err)
	}
	deleting(false)

	d.err = nil
	got, err := f.svc.Delete(ctx, f.admin, "infra", kernel.ExpectVersions(st.Version), true)
	if err != nil || !got.Deleting || got.Version != st.Version ||
		!slices.Equal(d.asked, []kernel.ULID{st.ID}) {
		t.Errorf("Delete with destroy = %+v, %v; asked %v", got, err, d.asked)
	}
	deleting(true)
	if got, err := f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{}, true); err != nil ||
		!got.Deleting || len(d.asked) != 1 {
		t.Errorf("a second destroy = %+v, %v; asked %v", got.Deleting, err, d.asked)
	}
	if _, err := f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{}, false); !errors.Is(err,
		core.ErrDeleting) {
		t.Errorf("a delete of a deleting stack = %v", err)
	}

	if err := f.svc.Destroyed(ctx, f.acct, st.ID, false); err != nil {
		t.Fatal(err)
	}
	deleting(false)
	if err := f.svc.Destroyed(ctx, f.acct, st.ID, true); err != nil {
		t.Fatal(err)
	}
	deleting(false)

	g := &guard{}
	f.svc.Guard(g)
	if _, err := f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{}, true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Destroyed(ctx, f.acct, st.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Stack(ctx, f.acct, "infra"); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("a stack whose destroy run finished reads %v", err)
	}
	if len(g.deleted) != 1 || g.deleted[0].ID != st.ID {
		t.Errorf("Deleting was asked %v", g.deleted)
	}
	if err := f.svc.Destroyed(ctx, f.acct, st.ID, true); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("Destroyed of a deleted stack = %v", err)
	}

	in := input("Kept")
	in.ProtectFromDeletion = new(true)
	if _, err := f.svc.Create(ctx, f.admin, "", false, in); err != nil {
		t.Fatal(err)
	}
	for _, destroy := range []bool{false, true} {
		_, err := f.svc.Delete(ctx, f.admin, "kept", kernel.Expect{}, destroy)
		if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
			t.Errorf("a delete of a protected stack with destroy %v = %v", destroy, err)
		}
	}
	if err := pg.New(f.db).SetDeleting(ctx, f.acct, kernel.NewULID(), true); !errors.Is(err,
		stacksapi.ErrNotFound) {
		t.Errorf("SetDeleting of a missing stack = %v", err)
	}
}

// TestAccHandlerDestroy checks stackDelete with destroyResources: it
// answers the stack deleting and not deleted, a delete without it is then
// 409 invalid-state, and a destroy run the runs domain refuses is 409
// invalid-state with its detail, or resource-locked naming the holder of
// the lock that refuses it.
func TestAccHandlerDestroy(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	s := http.New(f.svc, f.authz, f.spaces)
	admin := kernel.WithPrincipal(ctx, f.admin)
	if _, err := f.svc.Create(ctx, f.admin, "", false, input("Infra")); err != nil {
		t.Fatal(err)
	}
	d := &destroyer{err: &stacksapi.Undestroyable{Detail: "The stack is disabled."}}
	f.svc.BindDestroyer(d)
	destroy := api.StackDeleteRequestObject{
		StackId: "infra", Params: api.StackDeleteParams{DestroyResources: new(true)},
	}
	_, err := s.StackDelete(admin, destroy)
	if !isProblem(err, problem.InvalidState) ||
		problemOf(t, err).Detail != "The stack is disabled." {
		t.Errorf("a refused destroy = %v", err)
	}

	d.err = &stacksapi.Undestroyable{Detail: "The stack is locked by bob.", LockedBy: "bob"}
	_, err = s.StackDelete(admin, destroy)
	if !isProblem(err, problem.ResourceLocked) || problemOf(t, err).LockedBy != "bob" {
		t.Errorf("a destroy another caller's lock refuses = %v", err)
	}

	d.err = nil
	answer, err := s.StackDelete(admin, destroy)
	if err != nil {
		t.Fatal(err)
	}
	if v := answer.(api.StackDelete200JSONResponse).Body; v.Deleted || !v.Deleting {
		t.Errorf("a destroy answers %+v", v)
	}
	read, err := s.Stack(admin, api.StackRequestObject{StackId: "infra"})
	if err != nil || !read.(api.Stack200JSONResponse).Body.Deleting {
		t.Errorf("the deleting stack reads %+v, %v", read, err)
	}
	_, err = s.StackDelete(admin, api.StackDeleteRequestObject{StackId: "infra"})
	if !isProblem(err, problem.InvalidState) {
		t.Errorf("a delete of a deleting stack = %v", err)
	}
}
