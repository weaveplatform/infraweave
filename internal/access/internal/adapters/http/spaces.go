package http

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// spacesList names the spaces list's cursors.
const spacesList = "spaces"

// Spaces lists the spaces of the caller's account it may read, by slug,
// keeping those where it may perform every action input.actions lists.
func (s Server) Spaces(
	ctx context.Context,
	req api.SpacesRequestObject,
) (api.SpacesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.spaces.Spaces(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	readable, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	var want []api.Action
	if req.Params.Input != nil && req.Params.Input.Actions != nil {
		want = *req.Params.Input.Actions
	}
	slugs := slugsOf(all)
	slices.SortFunc(all, func(a, b space.Space) int { return strings.Compare(a.Slug, b.Slug) })
	views := []api.Space{}
	for _, sp := range all {
		if !slices.Contains(readable, sp.ID) {
			continue
		}
		v, err := s.spaceView(ctx, p, sp, slugs)
		if err != nil {
			return nil, err
		}
		if !containsAll(v.AllowedActions, want) {
			continue
		}
		views = append(views, v)
	}
	items, next, err := listPage(spacesList, req.Params.Cursor, req.Params.Limit,
		func(c string) (string, error) { return c, nil },
		func(after string, n int) ([]api.Space, error) {
			i, _ := slices.BinarySearchFunc(views, after, func(v api.Space, slug string) int {
				return strings.Compare(v.Id, slug)
			})
			if i < len(views) && views[i].Id == after {
				i++
			}
			return views[i:min(i+n, len(views))], nil
		},
		func(v api.Space) string { return v.Id })
	if err != nil {
		return nil, err
	}
	return api.Spaces200JSONResponse{Body: api.SpacePage{Items: items, NextCursor: next}}, nil
}

// Space answers one space of the caller's account. The route table has
// answered 404 for one the caller cannot read.
func (s Server) Space(
	ctx context.Context,
	req api.SpaceRequestObject,
) (api.SpaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	sp, err := s.spaces.Space(ctx, p.AccountID, req.SpaceId)
	if err != nil {
		return nil, spaceError(err, req.SpaceId)
	}
	v, err := s.view(ctx, p, sp)
	if err != nil {
		return nil, err
	}
	return api.Space200JSONResponse{Body: v}, nil
}

// SpaceCreate creates a space below the body's parentSpace.
func (s Server) SpaceCreate(
	ctx context.Context,
	req api.SpaceCreateRequestObject,
) (api.SpaceCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := req.Body
	sp, err := s.spaces.Create(ctx, p, in.ParentSpace, spaceOf(*in))
	if err != nil {
		return nil, spaceError(err, in.ParentSpace)
	}
	v, err := s.view(ctx, p, sp)
	if err != nil {
		return nil, err
	}
	return api.SpaceCreate201JSONResponse{Body: v}, nil
}

// SpaceUpdate changes a space's fields and moves it below the body's
// parentSpace.
func (s Server) SpaceUpdate(
	ctx context.Context,
	req api.SpaceUpdateRequestObject,
) (api.SpaceUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := req.Body
	sp, err := s.spaces.Update(ctx, p, req.SpaceId, in.ParentSpace, spaceOf(*in))
	if err != nil {
		// A missing space was answered by the route table, so a missing
		// one here is the parent the body names.
		return nil, spaceError(err, in.ParentSpace)
	}
	v, err := s.view(ctx, p, sp)
	if err != nil {
		return nil, err
	}
	return api.SpaceUpdate200JSONResponse{Body: v}, nil
}

// SpaceDelete deletes a space and answers it as it was, with the
// caller's access to it then.
func (s Server) SpaceDelete(
	ctx context.Context,
	req api.SpaceDeleteRequestObject,
) (api.SpaceDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	// The view is read first: once the space is gone, it holds no grant.
	sp, err := s.spaces.Space(ctx, p.AccountID, req.SpaceId)
	if err != nil {
		return nil, spaceError(err, req.SpaceId)
	}
	v, err := s.view(ctx, p, sp)
	if err != nil {
		return nil, err
	}
	if _, err := s.spaces.Delete(ctx, p, req.SpaceId); err != nil {
		return nil, spaceError(err, req.SpaceId)
	}
	return api.SpaceDelete200JSONResponse{Body: v}, nil
}

// AllowNonAdminSpaceCreation answers the account's setting.
func (s Server) AllowNonAdminSpaceCreation(
	ctx context.Context,
	_ api.AllowNonAdminSpaceCreationRequestObject,
) (api.AllowNonAdminSpaceCreationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	on, err := s.spaces.NonAdminCreation(ctx, p.AccountID)
	if err != nil {
		return nil, settingError(err)
	}
	return api.AllowNonAdminSpaceCreation200JSONResponse{Body: on}, nil
}

// AccountToggleAllowNonAdminSpaceCreation flips the account's setting
// and answers the new value.
func (s Server) AccountToggleAllowNonAdminSpaceCreation(
	ctx context.Context,
	_ api.AccountToggleAllowNonAdminSpaceCreationRequestObject,
) (api.AccountToggleAllowNonAdminSpaceCreationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	on, err := s.spaces.ToggleNonAdminCreation(ctx, p)
	if err != nil {
		return nil, settingError(err)
	}
	return api.AccountToggleAllowNonAdminSpaceCreation200JSONResponse{Body: on}, nil
}

// view is sp as the caller sees it, naming its parent by slug.
func (s Server) view(ctx context.Context, p kernel.Principal, sp space.Space) (api.Space, error) {
	slugs := map[kernel.ULID]string{}
	if !sp.Top() {
		all, err := s.spaces.Spaces(ctx, p.AccountID)
		if err != nil {
			return api.Space{}, err //nolint:wrapcheck // the store marks an unreachable database
		}
		slugs = slugsOf(all)
	}
	return s.spaceView(ctx, p, sp, slugs)
}

// spaceView is sp with the actions p holds there, as the evaluator
// explains them, and the access level the highest space action among
// them gives; slugs names each space's parent.
func (s Server) spaceView(
	ctx context.Context,
	p kernel.Principal,
	sp space.Space,
	slugs map[kernel.ULID]string,
) (api.Space, error) {
	held, err := s.authz.Actions(ctx, p, sp.ID)
	if err != nil {
		return api.Space{}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	v := api.Space{
		AccessLevel:     accessLevel(held),
		AllowedActions:  actions(held),
		Description:     sp.Description,
		Id:              sp.Slug,
		InheritEntities: sp.InheritEntities,
		Labels:          nonNil(sp.Labels),
		Name:            sp.Name,
	}
	if !sp.Top() {
		parent := slugs[sp.ParentID]
		v.ParentSpace = &parent
	}
	return v, nil
}

// accessLevel is the reference's level for the highest of SPACE_ADMIN,
// SPACE_WRITE and SPACE_READ that held holds.
func accessLevel(held []accessapi.Action) api.SpaceAccessLevel {
	for _, l := range []struct {
		action accessapi.Action
		level  api.SpaceAccessLevel
	}{
		{accessapi.ActionSpaceAdmin, api.SpaceAccessLevelADMIN},
		{accessapi.ActionSpaceWrite, api.SpaceAccessLevelWRITE},
		{accessapi.ActionSpaceRead, api.SpaceAccessLevelREAD},
	} {
		if slices.Contains(held, l.action) {
			return l.level
		}
	}
	return api.SpaceAccessLevelNONE
}

func slugsOf(spaces []space.Space) map[kernel.ULID]string {
	out := make(map[kernel.ULID]string, len(spaces))
	for _, sp := range spaces {
		out[sp.ID] = sp.Slug
	}
	return out
}

func containsAll(held, want []api.Action) bool {
	for _, a := range want {
		if !slices.Contains(held, a) {
			return false
		}
	}
	return true
}

// spaceOf is the fields of a SpaceInput the service writes.
func spaceOf(in api.SpaceInput) space.Space {
	sp := space.Space{
		Name: in.Name, Description: in.Description, InheritEntities: in.InheritEntities,
	}
	if in.Labels != nil {
		sp.Labels = *in.Labels
	}
	return sp
}

// spaceError answers the space service's refusals; slug is the space a
// not found error names.
func spaceError(err error, slug string) error {
	if d, ok := errors.AsType[*space.Denied](err); ok {
		p := problem.Forbidden.New(d.Reason)
		p.Action = string(d.Action)
		return &problem.Error{Problem: p}
	}
	if r, ok := errors.AsType[*space.Refusal](err); ok {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.ValidationFailed.Error(
			"The space change is refused: " + strings.Join(r.Reasons, "; ") + ".")
	}
	switch {
	case errors.Is(err, space.ErrName):
		//nolint:wrapcheck // as above
		return problem.ValidationFailed.Error("A space needs a name.")
	case errors.Is(err, port.ErrNotFound):
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf("No space %q in this account.", slug))
	}
	return err
}
