package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
)

// TestAccBindingHandlers drives each stack binding operation and the
// space's access view through the domain's partial, as the route table
// hands it a request it has authorized, and checks each view and refusal.
func TestAccBindingHandlers(t *testing.T) {
	f := bindingFixture(t)
	s := http.New(f.svc, f.authz, f.spaces)
	admin := kernel.WithPrincipal(t.Context(), f.admin)
	writer := kernel.WithPrincipal(t.Context(), f.writer)

	created, err := s.StackRoleBindingCreate(admin, api.StackRoleBindingCreateRequestObject{
		Body: &api.StackRoleBindingInput{StackID: "infra", RoleID: "space-reader", SpaceID: "team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := created.(api.StackRoleBindingCreate201JSONResponse).Body
	if b.StackID != "infra" || b.StackName != "Infra" || b.Role.Id != "space-reader" ||
		b.Space.Id != "team" || b.Kind != api.StackRoleBindingKindStackRoleBinding {
		t.Errorf("created %+v", b)
	}
	batch, err := s.StackRoleBindingBatchCreate(admin, api.StackRoleBindingBatchCreateRequestObject{
		Body: &api.StackRoleBindingBatchCreateJSONRequestBody{Bindings: []api.StackRoleBindingInput{
			{StackID: "infra", RoleID: "space-writer", SpaceID: "team"},
			{StackID: "top", RoleID: "space-reader", SpaceID: "team"},
		}},
	})
	if err != nil || len(batch.(api.StackRoleBindingBatchCreate201JSONResponse).Body) != 2 {
		t.Fatalf("batch = %+v, %v", batch, err)
	}

	got, err := s.StackRoleBinding(
		admin,
		api.StackRoleBindingRequestObject{StackRoleBindingId: b.Id},
	)
	if r, ok := got.(api.StackRoleBinding200JSONResponse); err != nil || !ok ||
		r.Body.Id != b.Id || *r.Headers.ETag != `"1"` {
		t.Errorf("read %+v, %v", got, err)
	}
	updated, err := s.StackRoleBindingUpdate(admin, api.StackRoleBindingUpdateRequestObject{
		StackRoleBindingId: b.Id, Params: api.StackRoleBindingUpdateParams{IfMatch: new(`"1"`)},
		Body: &api.StackRoleBindingUpdateInput{RoleID: "space-admin", SpaceID: "team"},
	})
	if r, ok := updated.(api.StackRoleBindingUpdate200JSONResponse); err != nil || !ok ||
		r.Body.Role.Id != "space-admin" || *r.Headers.ETag != `"2"` {
		t.Errorf("updated %+v, %v", updated, err)
	}
	patched, err := s.StackRoleBindingPatch(admin, api.StackRoleBindingPatchRequestObject{
		StackRoleBindingId: b.Id, Body: &api.MergePatch{"roleID": "space-reader"},
	})
	if r, ok := patched.(api.StackRoleBindingPatch200JSONResponse); err != nil || !ok ||
		r.Body.Role.Id != "space-reader" || r.Body.Space.Id != "team" || *r.Headers.ETag != `"3"` {
		t.Errorf("patched %+v, %v", patched, err)
	}
	if _, err := s.StackRoleBindingPatch(admin, api.StackRoleBindingPatchRequestObject{
		StackRoleBindingId: kernel.NewULID().String(), Body: &api.MergePatch{},
	}); problemOf(t, err).Status != 404 {
		t.Errorf("a patch of no binding = %v", err)
	}

	first, err := s.StackRoleBindings(admin, api.StackRoleBindingsRequestObject{
		StackId: "infra", Params: api.StackRoleBindingsParams{Limit: new(int32(1))},
	})
	page := first.(api.StackRoleBindings200JSONResponse).Body
	if err != nil || len(page.Items) != 1 || page.Items[0].Id != b.Id || page.NextCursor == nil {
		t.Fatalf("first page %+v, %v", page, err)
	}
	rest, err := s.StackRoleBindings(admin, api.StackRoleBindingsRequestObject{
		StackId: "infra", Params: api.StackRoleBindingsParams{Cursor: page.NextCursor},
	})
	page = rest.(api.StackRoleBindings200JSONResponse).Body
	if err != nil || len(page.Items) != 1 || page.Items[0].Role.Id != "space-writer" ||
		page.NextCursor != nil {
		t.Errorf("last page %+v, %v", page, err)
	}

	view, err := s.StacksWithAccessToSpace(admin, api.StacksWithAccessToSpaceRequestObject{
		Params: api.StacksWithAccessToSpaceParams{
			Space: "team", Mode: new(api.ALLWITHACCESS), Input: api.SearchInput{},
		},
	})
	out := view.(api.StacksWithAccessToSpace200JSONResponse).Body
	if err != nil || len(out.Edges) != 2 || out.Edges[0].Node.Stack.Id != "infra" ||
		out.Edges[0].Node.Roles[0].Binding != nil || out.Edges[0].Node.Roles[0].Space.Id != "team" ||
		out.Edges[1].Node.Roles[0].Binding == nil || out.Edges[1].Node.Stack.Labels == nil {
		t.Errorf("all with access %+v, %v", out, err)
	}
	view, err = s.StacksWithAccessToSpace(writer, api.StacksWithAccessToSpaceRequestObject{
		Params: api.StacksWithAccessToSpaceParams{Space: "team"},
	})
	if out := view.(api.StacksWithAccessToSpace200JSONResponse).Body; err != nil ||
		len(out.Edges) != 1 || out.Edges[0].Node.Roles[0].Source != api.RESIDENT {
		t.Errorf("residents %+v, %v", out, err)
	}
	sugg, err := s.StacksWithAccessToSpaceSuggestions(admin,
		api.StacksWithAccessToSpaceSuggestionsRequestObject{
			Params: api.StacksWithAccessToSpaceSuggestionsParams{
				Space: "team", Mode: new(api.ALLWITHACCESS),
			},
		})
	if r, ok := sugg.(api.StacksWithAccessToSpaceSuggestions200JSONResponse); err != nil || !ok ||
		r.Body.FilteredCount != 2 || r.Body.Fields == nil {
		t.Errorf("suggestions %+v, %v", sugg, err)
	}

	deleted, err := s.StackRoleBindingDelete(admin, api.StackRoleBindingDeleteRequestObject{
		StackRoleBindingId: b.Id,
	})
	if r, ok := deleted.(api.StackRoleBindingDelete200JSONResponse); err != nil || !ok ||
		r.Body.Id != b.Id {
		t.Errorf("deleted %+v, %v", deleted, err)
	}

	for name, c := range map[string]struct {
		call func() error
		want problem.Type
	}{
		"a read of a deleted binding": {func() error {
			_, err := s.StackRoleBinding(admin, api.StackRoleBindingRequestObject{StackRoleBindingId: b.Id})
			return err
		}, problem.NotFound},
		"a held binding": {func() error {
			_, err := s.StackRoleBindingCreate(admin, api.StackRoleBindingCreateRequestObject{
				Body: &api.StackRoleBindingInput{StackID: "infra", RoleID: "space-writer", SpaceID: "team"},
			})
			return err
		}, problem.AlreadyExists},
		"a space the caller does not administer": {func() error {
			_, err := s.StackRoleBindingCreate(writer, api.StackRoleBindingCreateRequestObject{
				Body: &api.StackRoleBindingInput{StackID: "infra", RoleID: "space-reader", SpaceID: "team"},
			})
			return err
		}, problem.Forbidden},
		"no stack": {func() error {
			_, err := s.StackRoleBindingCreate(admin, api.StackRoleBindingCreateRequestObject{
				Body: &api.StackRoleBindingInput{StackID: "nope", RoleID: "space-reader", SpaceID: "team"},
			})
			return err
		}, problem.NotFound},
		"a batch the caller may not make": {func() error {
			_, err := s.StackRoleBindingBatchCreate(writer, api.StackRoleBindingBatchCreateRequestObject{
				Body: &api.StackRoleBindingBatchCreateJSONRequestBody{Bindings: []api.StackRoleBindingInput{
					{StackID: "top", RoleID: "space-reader", SpaceID: "team"},
				}},
			})
			return err
		}, problem.ValidationFailed},
		"an update of no binding": {func() error {
			_, err := s.StackRoleBindingUpdate(admin, api.StackRoleBindingUpdateRequestObject{
				StackRoleBindingId: b.Id,
				Body:               &api.StackRoleBindingUpdateInput{RoleID: "space-reader", SpaceID: "team"},
			})
			return err
		}, problem.NotFound},
		"a delete of no binding": {func() error {
			_, err := s.StackRoleBindingDelete(admin, api.StackRoleBindingDeleteRequestObject{
				StackRoleBindingId: b.Id,
			})
			return err
		}, problem.NotFound},
		"the bindings of no stack": {func() error {
			_, err := s.StackRoleBindings(admin, api.StackRoleBindingsRequestObject{StackId: "nope"})
			return err
		}, problem.NotFound},
		"a cursor of another list": {func() error {
			_, err := s.StackRoleBindings(admin, api.StackRoleBindingsRequestObject{
				StackId: "infra", Params: api.StackRoleBindingsParams{Cursor: new("nope")},
			})
			return err
		}, problem.InvalidCursor},
		"all with access for a writer": {func() error {
			_, err := s.StacksWithAccessToSpace(writer, api.StacksWithAccessToSpaceRequestObject{
				Params: api.StacksWithAccessToSpaceParams{Space: "team", Mode: new(api.ALLWITHACCESS)},
			})
			return err
		}, problem.Forbidden},
		"suggestions of no space": {func() error {
			_, err := s.StacksWithAccessToSpaceSuggestions(admin,
				api.StacksWithAccessToSpaceSuggestionsRequestObject{
					Params: api.StacksWithAccessToSpaceSuggestionsParams{Space: "nowhere"},
				})
			return err
		}, problem.NotFound},
	} {
		// A *kernel.Invalid is answered as validation-failed after the
		// handler, by the strict server's error handler.
		err := c.call()
		if _, invalid := errors.AsType[*kernel.Invalid](err); !isProblem(err, c.want) &&
			(c.want != problem.ValidationFailed || !invalid) {
			t.Errorf("%s = %v, want %s", name, err, c.want.New("").Type)
		}
	}

	f.spaces.treeErr = errDown
	if _, err := s.StacksWithAccessToSpace(admin, api.StacksWithAccessToSpaceRequestObject{
		Params: api.StacksWithAccessToSpaceParams{Space: "team"},
	}); err == nil {
		t.Error("the access view without the tree answered")
	}
	f.spaces.treeErr = nil
	f.spaces.slugsErr = errDown
	if _, err := s.StacksWithAccessToSpace(admin, api.StacksWithAccessToSpaceRequestObject{
		Params: api.StacksWithAccessToSpaceParams{Space: "team"},
	}); err == nil {
		t.Error("the access view without the slugs answered")
	}
}

// TestAccBindingHandlersUnauthenticated checks that each binding
// operation refuses a request that carries no principal.
func TestAccBindingHandlersUnauthenticated(t *testing.T) {
	f := newFixture(t)
	s := http.New(f.svc, f.authz, f.spaces)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"read": func() error {
			_, err := s.StackRoleBinding(ctx, api.StackRoleBindingRequestObject{})
			return err
		},
		"create": func() error {
			_, err := s.StackRoleBindingCreate(ctx, api.StackRoleBindingCreateRequestObject{})
			return err
		},
		"batch": func() error {
			_, err := s.StackRoleBindingBatchCreate(ctx, api.StackRoleBindingBatchCreateRequestObject{})
			return err
		},
		"update": func() error {
			_, err := s.StackRoleBindingUpdate(ctx, api.StackRoleBindingUpdateRequestObject{})
			return err
		},
		"patch": func() error {
			_, err := s.StackRoleBindingPatch(ctx, api.StackRoleBindingPatchRequestObject{})
			return err
		},
		"delete": func() error {
			_, err := s.StackRoleBindingDelete(ctx, api.StackRoleBindingDeleteRequestObject{})
			return err
		},
		"list": func() error {
			_, err := s.StackRoleBindings(ctx, api.StackRoleBindingsRequestObject{})
			return err
		},
		"access": func() error {
			_, err := s.StacksWithAccessToSpace(ctx, api.StacksWithAccessToSpaceRequestObject{})
			return err
		},
		"suggestions": func() error {
			_, err := s.StacksWithAccessToSpaceSuggestions(ctx,
				api.StacksWithAccessToSpaceSuggestionsRequestObject{})
			return err
		},
	} {
		if err := call(); !isProblem(err, problem.Unauthenticated) {
			t.Errorf("%s = %v", name, err)
		}
	}
}
