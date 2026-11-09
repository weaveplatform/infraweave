package http_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	vhttp "github.com/weaveplatform/infraweave/internal/vendors/internal/http"
)

// catalog lists versions newest first and resolves every constraint to
// the vendor's newest, or fails with err.
type catalog struct {
	versions map[vendorsapi.Vendor][]string
	err      error
}

func (c catalog) Versions(_ context.Context, kind vendorsapi.Vendor) ([]string, error) {
	return c.versions[kind], c.err
}

func (c catalog) Effective(
	_ context.Context,
	kind vendorsapi.Vendor,
	constraints string,
) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	return string(kind) + " " + constraints, nil
}

func isProblem(err error, typ problem.Type) bool {
	pe, ok := errors.AsType[*problem.Error](err)
	return ok && pe.Problem.Type == typ.New("").Type
}

// TestUnitVersions pages each vendor's versions newest first, from a
// cursor naming the last version of the page before, and refuses a
// cursor of the other vendor's list or one naming no version.
func TestUnitVersions(t *testing.T) {
	ctx := t.Context()
	s := vhttp.New(catalog{versions: map[vendorsapi.Vendor][]string{
		vendorsapi.Terraform: {"1.6.0", "1.5.7", "1.5.6", "1.5.0"},
		vendorsapi.OpenTofu:  {"1.8.0", "1.6.0"},
	}})
	var got []string
	var cursor *string
	for range 3 {
		res, err := s.TerraformVersions(ctx, api.TerraformVersionsRequestObject{
			Params: api.TerraformVersionsParams{Cursor: cursor, Limit: new(int32(3))},
		})
		if err != nil {
			t.Fatal(err)
		}
		body := res.(api.TerraformVersions200JSONResponse).Body
		got = append(got, body.Items...)
		if cursor = body.NextCursor; cursor == nil {
			break
		}
	}
	if !slices.Equal(got, []string{"1.6.0", "1.5.7", "1.5.6", "1.5.0"}) {
		t.Errorf("pages %v", got)
	}

	first, err := s.TerraformVersions(ctx, api.TerraformVersionsRequestObject{
		Params: api.TerraformVersionsParams{Limit: new(int32(1))},
	})
	if err != nil {
		t.Fatal(err)
	}
	tfCursor := first.(api.TerraformVersions200JSONResponse).Body.NextCursor
	if _, err := s.OpenTofuVersions(ctx, api.OpenTofuVersionsRequestObject{
		Params: api.OpenTofuVersionsParams{Cursor: tfCursor},
	}); !isProblem(err, problem.InvalidCursor) {
		t.Errorf("a Terraform cursor of OpenTofu = %v", err)
	}
	res, err := s.OpenTofuVersions(ctx, api.OpenTofuVersionsRequestObject{})
	if err != nil || !slices.Equal(res.(api.OpenTofuVersions200JSONResponse).Body.Items,
		[]string{"1.8.0", "1.6.0"}) {
		t.Errorf("OpenTofu %+v, %v", res, err)
	}

	// A cursor past every listed version, as one of a release no longer
	// listed would be, reads an empty page.
	odd := encode(t, "terraform-versions", "1.0.0")
	res2, err := s.TerraformVersions(ctx, api.TerraformVersionsRequestObject{
		Params: api.TerraformVersionsParams{Cursor: &odd},
	})
	if err != nil || len(res2.(api.TerraformVersions200JSONResponse).Body.Items) != 0 {
		t.Errorf("past the last version %+v, %v", res2, err)
	}
	bad := encode(t, "terraform-versions", "latest")
	if _, err := s.TerraformVersions(ctx, api.TerraformVersionsRequestObject{
		Params: api.TerraformVersionsParams{Cursor: &bad},
	}); !isProblem(err, problem.InvalidCursor) {
		t.Errorf("a cursor naming no version = %v", err)
	}

	down := vhttp.New(catalog{err: errors.New("down")})
	if _, err := down.TerraformVersions(ctx, api.TerraformVersionsRequestObject{}); err == nil {
		t.Error("TerraformVersions on a failing catalogue succeeded")
	}
	if _, err := down.OpenTofuVersions(ctx, api.OpenTofuVersionsRequestObject{}); err == nil {
		t.Error("OpenTofuVersions on a failing catalogue succeeded")
	}
}

// TestUnitEffective answers each vendor's resolution, and the catalogue's
// refusal as it is.
func TestUnitEffective(t *testing.T) {
	ctx := t.Context()
	s := vhttp.New(catalog{})
	tf, err := s.TerraformEffectiveVersion(ctx, api.TerraformEffectiveVersionRequestObject{
		Params: api.TerraformEffectiveVersionParams{Constraints: "~> 1.5"},
	})
	if err != nil || tf.(api.TerraformEffectiveVersion200JSONResponse).Body != "terraform ~> 1.5" {
		t.Errorf("Terraform %+v, %v", tf, err)
	}
	tofu, err := s.OpenTofuEffectiveVersion(ctx, api.OpenTofuEffectiveVersionRequestObject{
		Params: api.OpenTofuEffectiveVersionParams{Constraints: "1.6.0"},
	})
	if err != nil || tofu.(api.OpenTofuEffectiveVersion200JSONResponse).Body != "opentofu 1.6.0" {
		t.Errorf("OpenTofu %+v, %v", tofu, err)
	}
	down := vhttp.New(catalog{err: errors.New("down")})
	if _, err := down.TerraformEffectiveVersion(
		ctx,
		api.TerraformEffectiveVersionRequestObject{},
	); err == nil {
		t.Error("TerraformEffectiveVersion on a failing catalogue succeeded")
	}
	if _, err := down.OpenTofuEffectiveVersion(
		ctx,
		api.OpenTofuEffectiveVersionRequestObject{},
	); err == nil {
		t.Error("OpenTofuEffectiveVersion on a failing catalogue succeeded")
	}
}

// encode is a cursor of list naming version v.
func encode(t *testing.T, list, v string) string {
	t.Helper()
	c, err := page.Encode(list, kernel.Cursor[string]{Key: v, ID: v})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
