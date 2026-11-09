package http

import (
	"context"
	"slices"

	"github.com/weaveplatform/infraweave/internal/iac/version"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
)

// Server is the vendors domain's partial of the API's strict server. Any
// authenticated caller reads the catalogue, which names no account's data.
type Server struct{ catalog vendorsapi.Catalog }

// New is the partial over catalog.
func New(catalog vendorsapi.Catalog) Server { return Server{catalog: catalog} }

// TerraformVersions lists the Terraform versions, newest first.
func (s Server) TerraformVersions(
	ctx context.Context,
	req api.TerraformVersionsRequestObject,
) (api.TerraformVersionsResponseObject, error) {
	items, next, err := s.page(ctx, vendorsapi.Terraform, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	return api.TerraformVersions200JSONResponse{
		Body: api.TerraformVersionsPage{Items: items, NextCursor: next},
	}, nil
}

// OpenTofuVersions lists the OpenTofu versions, newest first.
func (s Server) OpenTofuVersions(
	ctx context.Context,
	req api.OpenTofuVersionsRequestObject,
) (api.OpenTofuVersionsResponseObject, error) {
	items, next, err := s.page(ctx, vendorsapi.OpenTofu, req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	return api.OpenTofuVersions200JSONResponse{
		Body: api.OpenTofuVersionsPage{Items: items, NextCursor: next},
	}, nil
}

// TerraformEffectiveVersion resolves the constraints to a Terraform
// version.
func (s Server) TerraformEffectiveVersion(
	ctx context.Context,
	req api.TerraformEffectiveVersionRequestObject,
) (api.TerraformEffectiveVersionResponseObject, error) {
	v, err := s.catalog.Effective(ctx, vendorsapi.Terraform, req.Params.Constraints)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *kernel.Invalid, or the store's read
	}
	return api.TerraformEffectiveVersion200JSONResponse{Body: v}, nil
}

// OpenTofuEffectiveVersion resolves the constraints to an OpenTofu
// version.
func (s Server) OpenTofuEffectiveVersion(
	ctx context.Context,
	req api.OpenTofuEffectiveVersionRequestObject,
) (api.OpenTofuEffectiveVersionResponseObject, error) {
	v, err := s.catalog.Effective(ctx, vendorsapi.OpenTofu, req.Params.Constraints)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return api.OpenTofuEffectiveVersion200JSONResponse{Body: v}, nil
}

// page is a page of kind's versions after cursor's, and the cursor of the
// next when there is one. A cursor names the last version of its page, so
// a release published between two pages appears on neither and shifts
// nothing.
func (s Server) page(
	ctx context.Context,
	kind vendorsapi.Vendor,
	cursor *string,
	limit *int32,
) ([]string, *string, error) {
	list := listOf(kind)
	var after *version.Version
	if cursor != nil {
		c, err := page.Decode[string](list, *cursor)
		if err != nil {
			return nil, nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		v, err := version.Parse(c.ID)
		if err != nil {
			//nolint:wrapcheck // as above
			return nil, nil, problem.InvalidCursor.Error("The cursor names no version.")
		}
		after = &v
	}
	versions, err := s.catalog.Versions(ctx, kind)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the store names the read
	}
	rest := versions
	if after != nil {
		i := slices.IndexFunc(versions, func(raw string) bool {
			v, err := version.Parse(raw)
			return err == nil && v.Compare(*after) < 0
		})
		if i < 0 {
			i = len(versions)
		}
		rest = versions[i:]
	}
	n := int(kernel.PageLimit(limit))
	items := slices.Clone(rest[:min(len(rest), n)])
	if len(rest) <= n {
		return items, nil, nil
	}
	last := items[n-1]
	next, err := page.Encode(list, kernel.Cursor[string]{Key: last, ID: last})
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // encoding a string cannot fail
	}
	return items, &next, nil
}

// listOf is the list name kind's cursors carry, so one vendor's cursor is
// refused by the other's list.
func listOf(kind vendorsapi.Vendor) string { return string(kind) + "-versions" }
