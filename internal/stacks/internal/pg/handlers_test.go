package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
)

// problemOf is err's problem type and its body, failing when err is no
// problem.
func problemOf(t *testing.T, err error) problem.Problem {
	t.Helper()
	pe, ok := errors.AsType[*problem.Error](err)
	if !ok {
		t.Fatalf("err = %v, want a problem", err)
	}
	return pe.Problem
}

// TestAccHandlers drives each Stacks operation through the domain's
// partial over the fixture's service, as the route table hands it a
// request it has authorized, and checks the view and each refusal.
func TestAccHandlers(t *testing.T) {
	f := newFixture(t)
	s := http.New(f.svc, f.authz, f.spaces)
	admin := kernel.WithPrincipal(t.Context(), f.admin)
	writer := kernel.WithPrincipal(t.Context(), f.writer)
	reader := kernel.WithPrincipal(t.Context(), f.reader)
	tf := api.StackConfigVendorInput{Terraform: &api.StackConfigVendorTerraformInput{
		Version: new(">= 1.5, < 2.0"), Workspace: new("prod"),
	}}
	in := api.StackInput{
		Name: "Infra", Repository: "infra", Branch: "main", Space: new("team"),
		Labels: &[]string{"b", "a"}, BeforePlan: &[]string{"make"}, VendorConfig: &tf,
		Provider: new(api.VCSProviderGITLAB), Administrative: new(true),
	}
	created, err := s.StackCreate(admin, api.StackCreateRequestObject{
		Body: &api.StackCreateRequest{Input: in, ManageState: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	v := created.(api.StackCreate201JSONResponse).Body
	if v.Id != "infra" || v.Space != "team" || v.SpaceDetails.Id != "team" || v.Administrative ||
		v.Provider != api.VCSProviderGITLAB || !v.ManagesStateFile || v.State != api.StackStateNONE ||
		!reflect.DeepEqual(v.Labels, []string{"a", "b"}) ||
		!reflect.DeepEqual(v.Hooks.BeforePlan, []string{"make"}) || v.Hooks.AfterRun == nil ||
		v.AttachedContexts == nil || !v.CanWrite || len(v.AllowedActions) != 4 {
		t.Errorf("created %+v", v)
	}
	vendor, err := v.VendorConfig.AsStackConfigVendorTerraform()
	if err != nil || *vendor.Version != ">= 1.5, < 2.0" || *vendor.Workspace != "prod" ||
		vendor.WorkflowTool != api.TerraformWorkflowToolTERRAFORMFOSS {
		t.Errorf("vendorConfig %+v, %v", vendor, err)
	}

	in.TerraformVersion = new("1.9.0")
	in.Description = new("ours")
	updated, err := s.StackUpdate(admin, api.StackUpdateRequestObject{StackId: "infra", Body: &in})
	if err != nil {
		t.Fatal(err)
	}
	v = updated.(api.StackUpdate200JSONResponse).Body
	if vendor, _ := v.VendorConfig.AsStackConfigVendorTerraform(); *v.Description != "ours" ||
		*vendor.Version != "1.9.0" {
		t.Errorf("updated %+v, vendor %+v", v, vendor)
	}

	tofu := api.StackInput{
		Name: "Tofu", Repository: "infra", Branch: "main", Space: new("team"),
		VendorConfig: &api.StackConfigVendorInput{Opentofu: &api.StackConfigVendorOpenTofuInput{
			Concise: new(true),
		}},
	}
	created, err = s.StackCreate(admin, api.StackCreateRequestObject{
		Body: &api.StackCreateRequest{Input: tofu, Slug: new("tofu")},
	})
	if err != nil {
		t.Fatal(err)
	}
	ot, err := created.(api.StackCreate201JSONResponse).Body.VendorConfig.AsStackConfigVendorOpenTofu()
	if err != nil || !ot.Concise || ot.WorkflowTool != api.OpenTofuWorkflowToolOPENTOFU {
		t.Errorf("an OpenTofu stack's vendorConfig %+v, %v", ot, err)
	}

	listed, err := s.Stacks(
		reader,
		api.StacksRequestObject{Params: api.StacksParams{Limit: new(int32(1))}},
	)
	if err != nil {
		t.Fatal(err)
	}
	page := listed.(api.Stacks200JSONResponse).Body
	if len(page.Items) != 1 || page.Items[0].Id != "infra" || page.NextCursor == nil ||
		page.Items[0].CanWrite {
		t.Errorf("first page %+v", page)
	}
	listed, err = s.Stacks(reader, api.StacksRequestObject{Params: api.StacksParams{
		Cursor: page.NextCursor,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if page := listed.(api.Stacks200JSONResponse).Body; len(page.Items) != 1 ||
		page.Items[0].Id != "tofu" || page.NextCursor != nil {
		t.Errorf("second page %+v", page)
	}
	empty := kernel.WithPrincipal(t.Context(), kernel.Principal{AccountID: f.acct})
	listed, err = s.Stacks(empty, api.StacksRequestObject{})
	if err != nil || len(listed.(api.Stacks200JSONResponse).Body.Items) != 0 {
		t.Errorf("a list for a caller who reads nothing = %+v, %v", listed, err)
	}

	for _, c := range []struct {
		name string
		call func() (any, error)
	}{
		{"lock", func() (any, error) {
			return s.StackLock(writer, api.StackLockRequestObject{
				StackId: "infra", Body: &api.StackLockRequest{Note: new("mine")},
			})
		}},
		{"star", func() (any, error) {
			return s.StarSet(reader, api.StarSetRequestObject{
				StackId: "infra", Body: &api.StarSetRequest{Star: true},
			})
		}},
		{"disable", func() (any, error) {
			return s.StackDisable(admin, api.StackDisableRequestObject{StackId: "infra"})
		}},
		{"enable", func() (any, error) {
			return s.StackEnable(admin, api.StackEnableRequestObject{StackId: "infra"})
		}},
	} {
		if _, err := c.call(); err != nil {
			t.Errorf("%s = %v", c.name, err)
		}
	}
	read, err := s.Stack(reader, api.StackRequestObject{StackId: "infra"})
	if err != nil {
		t.Fatal(err)
	}
	if v := read.(api.Stack200JSONResponse).Body; !v.Starred || *v.LockedBy != "writer" ||
		*v.LockNote != "mine" || v.LockedAt == nil || v.IsDisabled {
		t.Errorf("read %+v", v)
	}
	_, err = s.StackLock(admin, api.StackLockRequestObject{StackId: "infra"})
	if p := problemOf(t, err); p.Status != 409 || p.LockedBy != "writer" {
		t.Errorf("a second lock = %+v", p)
	}
	_, err = s.StackUnlock(reader, api.StackUnlockRequestObject{StackId: "infra"})
	if p := problemOf(t, err); p.Status != 403 || p.Action != "STACK_UNLOCK_FORCE" {
		t.Errorf("another caller's unlock = %+v", p)
	}
	if _, err := s.StackUnlock(writer, api.StackUnlockRequestObject{StackId: "infra"}); err != nil {
		t.Errorf("the holder's unlock = %v", err)
	}

	_, err = s.StackCreate(admin, api.StackCreateRequestObject{
		Body: &api.StackCreateRequest{Input: in},
	})
	if p := problemOf(t, err); p.Status != 409 {
		t.Errorf("a create of a taken slug = %+v", p)
	}
	hidden := in
	hidden.Space = new("hidden")
	_, err = s.StackUpdate(admin, api.StackUpdateRequestObject{StackId: "infra", Body: &hidden})
	if p := problemOf(t, err); p.Status != 404 {
		t.Errorf("a move to a hidden space = %+v", p)
	}
	_, err = s.Stack(admin, api.StackRequestObject{StackId: "gone"})
	if p := problemOf(t, err); p.Status != 404 {
		t.Errorf("a missing stack = %+v", p)
	}
	bad := in
	bad.TerraformVersion = new("latest")
	_, err = s.StackCreate(admin, api.StackCreateRequestObject{
		Body: &api.StackCreateRequest{Input: bad, Slug: new("other")},
	})
	if inv, ok := errors.AsType[*kernel.Invalid](
		err,
	); !ok ||
		inv.Errors[0].Pointer != "/input/terraformVersion" {
		t.Errorf("a bad version = %v", err)
	}

	in.Name = "Infra Renamed"
	if _, err := s.StackUpdate(
		admin,
		api.StackUpdateRequestObject{StackId: "infra", Body: &in},
	); err != nil {
		t.Fatal(err)
	}
	reslugged, err := s.StackReslug(admin, api.StackReslugRequestObject{StackId: "infra"})
	if err != nil || reslugged.(api.StackReslug200JSONResponse).Body.Id != "infra-renamed" {
		t.Errorf("Reslug = %+v, %v", reslugged, err)
	}
	deleted, err := s.StackDelete(admin, api.StackDeleteRequestObject{StackId: "infra-renamed"})
	if err != nil {
		t.Fatal(err)
	}
	if v := deleted.(api.StackDelete200JSONResponse).Body; !v.Deleted || v.Deleting {
		t.Errorf("deleted %+v", v)
	}
	_, err = s.StackDelete(admin, api.StackDeleteRequestObject{StackId: "infra-renamed"})
	if p := problemOf(t, err); p.Status != 404 {
		t.Errorf("a second delete = %+v", p)
	}

	anonymous := context.Background()
	for name, call := range map[string]func() error{
		"stacks": func() error { _, err := s.Stacks(anonymous, api.StacksRequestObject{}); return err },
		"stack": func() error {
			_, err := s.Stack(anonymous, api.StackRequestObject{StackId: "tofu"})
			return err
		},
		"stackCreate": func() error {
			_, err := s.StackCreate(anonymous, api.StackCreateRequestObject{
				Body: &api.StackCreateRequest{Input: tofu},
			})
			return err
		},
		"stackUpdate": func() error {
			_, err := s.StackUpdate(anonymous, api.StackUpdateRequestObject{StackId: "tofu", Body: &tofu})
			return err
		},
		"stackDelete": func() error {
			_, err := s.StackDelete(anonymous, api.StackDeleteRequestObject{StackId: "tofu"})
			return err
		},
		"stackReslug": func() error {
			_, err := s.StackReslug(anonymous, api.StackReslugRequestObject{StackId: "tofu"})
			return err
		},
	} {
		if p := problemOf(t, call()); p.Status != 401 {
			t.Errorf("%s with no principal = %+v", name, p)
		}
	}
	if _, err := json.Marshal(v); err != nil {
		t.Errorf("the view does not encode: %v", err)
	}
}

// TestAccHandlersUnavailable checks that each operation fails, and not
// as a missing stack, once the database is gone.
func TestAccHandlersUnavailable(t *testing.T) {
	f := newFixture(t)
	s := http.New(f.svc, f.authz, f.spaces)
	ctx := kernel.WithPrincipal(t.Context(), f.admin)
	in := api.StackInput{Name: "Infra", Repository: "infra", Branch: "main", Space: new("team")}
	if _, err := s.StackCreate(ctx, api.StackCreateRequestObject{
		Body: &api.StackCreateRequest{Input: in},
	}); err != nil {
		t.Fatal(err)
	}
	f.db.Close()
	for name, call := range map[string]func() error{
		"stacks": func() error { _, err := s.Stacks(ctx, api.StacksRequestObject{}); return err },
		"stack": func() error {
			_, err := s.Stack(ctx, api.StackRequestObject{StackId: "infra"})
			return err
		},
		"stackCreate": func() error {
			_, err := s.StackCreate(ctx, api.StackCreateRequestObject{
				Body: &api.StackCreateRequest{Input: in, Slug: new("other")},
			})
			return err
		},
		"stackUpdate": func() error {
			_, err := s.StackUpdate(ctx, api.StackUpdateRequestObject{StackId: "infra", Body: &in})
			return err
		},
		"stackDelete": func() error {
			_, err := s.StackDelete(ctx, api.StackDeleteRequestObject{StackId: "infra"})
			return err
		},
		"stackDisable": func() error {
			_, err := s.StackDisable(ctx, api.StackDisableRequestObject{StackId: "infra"})
			return err
		},
		"stackEnable": func() error {
			_, err := s.StackEnable(ctx, api.StackEnableRequestObject{StackId: "infra"})
			return err
		},
		"starSet": func() error {
			_, err := s.StarSet(ctx, api.StarSetRequestObject{
				StackId: "infra", Body: &api.StarSetRequest{Star: true},
			})
			return err
		},
	} {
		err := call()
		if pe, ok := errors.AsType[*problem.Error](
			err,
		); err == nil ||
			ok && pe.Problem.Status == 404 {
			t.Errorf("%s on a closed database = %v", name, err)
		}
	}
}
