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
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
)

// Spaces lists the spaces of the caller's account it may read, by slug,
// keeping those where it may perform every action input.actions lists.
// The spaces it may not read, or where it may not perform one of them, are
// left out in the query, so every page before the last is full.
func (s Server) Spaces(
	ctx context.Context,
	req api.SpacesRequestObject,
) (api.SpacesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var want []api.Action
	if req.Params.Input != nil && req.Params.Input.Actions != nil {
		want = *req.Params.Input.Actions
	}
	reach, err := s.allowing(ctx, p, want)
	if err != nil {
		return nil, err
	}
	in := search.Input{First: req.Params.Limit, After: deref(req.Params.Cursor)}
	res, err := s.spaces.Search(ctx, p.AccountID, in, reach)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a bad cursor is a problem
	}
	all, err := s.spaces.Spaces(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	slugs := slugsOf(all)
	out := api.SpacePage{Items: []api.Space{}}
	for _, e := range res.Edges {
		v, err := s.spaceView(ctx, p, e.Node, slugs)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, v)
	}
	if res.HasNext {
		out.NextCursor = &res.End
	}
	return api.Spaces200JSONResponse{Body: out}, nil
}

// allowing is every space of p's account where p may read and perform
// each of want. An action no role may hold, being disabled, deprecated or
// no catalogue action, is allowed nowhere, as a space's allowedActions
// never list it.
func (s Server) allowing(
	ctx context.Context,
	p kernel.Principal,
	want []api.Action,
) ([]kernel.ULID, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	for _, a := range want {
		info, ok := accessapi.ActionOf(accessapi.Action(a))
		if !ok || !info.Assignable() {
			return nil, nil
		}
		in, err := s.authz.Reachable(ctx, p, info.Action)
		if err != nil {
			return nil, err //nolint:wrapcheck // as above
		}
		reach = slices.DeleteFunc(
			reach,
			func(id kernel.ULID) bool { return !slices.Contains(in, id) },
		)
	}
	return reach, nil
}

// SearchSpaces searches the spaces of the caller's account it may read.
func (s Server) SearchSpaces(
	ctx context.Context,
	req api.SearchSpacesRequestObject,
) (api.SearchSpacesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	res, err := s.spaces.Search(ctx, p.AccountID, apisearch.Input(*req.Body), reach)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	out := api.SearchSpacesOutput{
		Edges:    []api.SpaceConnection{},
		PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.SpaceConnection{
			Cursor: e.Cursor, Node: api.SpaceRef{Id: e.Node.Slug},
		})
	}
	return api.SearchSpaces200JSONResponse{Body: out}, nil
}

// SearchSpacesSuggestions lists the values of the spaces of the caller's
// account it may read.
func (s Server) SearchSpacesSuggestions(
	ctx context.Context,
	req api.SearchSpacesSuggestionsRequestObject,
) (api.SearchSpacesSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	got, err := s.spaces.Suggest(ctx, p.AccountID, apisearch.SuggestionsInput(*req.Body), reach)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	return api.SearchSpacesSuggestions200JSONResponse{Body: apisearch.Suggestions(got)}, nil
}

// ActionCatalogue lists every catalogue action with its flags.
func (s Server) ActionCatalogue(
	context.Context,
	api.ActionCatalogueRequestObject,
) (api.ActionCatalogueResponseObject, error) {
	catalogue := accessapi.Actions()
	out := make([]api.ActionCatalogueEntry, 0, len(catalogue))
	for _, a := range catalogue {
		out = append(out, api.ActionCatalogueEntry{
			Action: api.Action(a.Action), Disabled: a.Disabled, Deprecated: a.Deprecated,
		})
	}
	return api.ActionCatalogue200JSONResponse{Body: out}, nil
}

// Space answers one space of the caller's account, tagged with its
// version. The route table has answered 404 for one the caller cannot
// read.
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
	return api.Space200JSONResponse{
		Body: v, Headers: api.Space200ResponseHeaders{ETag: new(strict.ETag(sp.Version))},
	}, nil
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
// parentSpace, when its If-Match, if any, names the space's version.
func (s Server) SpaceUpdate(
	ctx context.Context,
	req api.SpaceUpdateRequestObject,
) (api.SpaceUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	sp, err := s.updateSpace(ctx, p, req.SpaceId, strict.IfMatch(req.Params.IfMatch), *req.Body)
	if err != nil {
		return nil, err
	}
	v, err := s.view(ctx, p, sp)
	if err != nil {
		return nil, err
	}
	return api.SpaceUpdate200JSONResponse{
		Body: v, Headers: api.SpaceUpdate200ResponseHeaders{ETag: new(strict.ETag(sp.Version))},
	}, nil
}

// SpacePatch changes the fields its merge patch names and keeps the rest,
// writing the result as SpaceUpdate writes its body.
func (s Server) SpacePatch(
	ctx context.Context,
	req api.SpacePatchRequestObject,
) (api.SpacePatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.SpaceInput, int64, error) {
		sp, err := s.spaces.Space(ctx, p.AccountID, req.SpaceId)
		if err != nil {
			return api.SpaceInput{}, 0, spaceError(err, req.SpaceId)
		}
		slugs, err := s.spaces.Slugs(ctx, p.AccountID)
		if err != nil {
			return api.SpaceInput{}, 0, err //nolint:wrapcheck // the store marks an unreachable database
		}
		return spaceInput(sp, slugs), sp.Version, nil
	}
	write := func(in api.SpaceInput, expect kernel.Expect) (space.Space, error) {
		return s.updateSpace(ctx, p, req.SpaceId, expect, in)
	}
	sp, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // read and write answer their problems
	}
	v, err := s.view(ctx, p, sp)
	if err != nil {
		return nil, err
	}
	return api.SpacePatch200JSONResponse{
		Body: v, Headers: api.SpacePatch200ResponseHeaders{ETag: new(strict.ETag(sp.Version))},
	}, nil
}

// updateSpace writes in to p's account's space slug, as SpaceUpdate and
// SpacePatch do, when expect accepts its version.
func (s Server) updateSpace(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	expect kernel.Expect,
	in api.SpaceInput,
) (space.Space, error) {
	sp, err := s.spaces.Update(ctx, p, slug, in.ParentSpace, expect, spaceOf(in))
	if err != nil {
		// A missing space was answered by the route table, so a missing
		// one here is the parent the body names.
		return space.Space{}, spaceError(err, in.ParentSpace)
	}
	return sp, nil
}

// SpaceDelete deletes a space, when its If-Match, if any, names the
// space's version, and answers it as it was, with the caller's access to
// it then.
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
	if _, err := s.spaces.Delete(ctx, p, req.SpaceId,
		strict.IfMatch(req.Params.IfMatch)); err != nil {
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

// spaceInput is sp as the SpaceInput that writes it, naming its parent by
// slug through slugs, and none for the top space.
func spaceInput(sp space.Space, slugs map[kernel.ULID]string) api.SpaceInput {
	in := api.SpaceInput{
		Name: sp.Name, Description: sp.Description, InheritEntities: sp.InheritEntities,
		Labels: new(nonNil(sp.Labels)),
	}
	if !sp.Top() {
		in.ParentSpace = slugs[sp.ParentID]
	}
	return in
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
