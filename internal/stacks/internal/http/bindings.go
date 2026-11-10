package http

import (
	"bytes"
	"context"
	"errors"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// bindingsList is the stack bindings list's cursor name.
const bindingsList = "stack-role-bindings"

// StackRoleBinding answers one of the account's stack role bindings.
func (s Server) StackRoleBinding(
	ctx context.Context,
	req api.StackRoleBindingRequestObject,
) (api.StackRoleBindingResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b, err := s.stacks.Binding(ctx, p.AccountID, req.StackRoleBindingId)
	if err != nil {
		return nil, bindingError(err, "")
	}
	return api.StackRoleBinding200JSONResponse{
		Body: bindingView(b), Headers: api.StackRoleBinding200ResponseHeaders{ETag: bindingTag(b)},
	}, nil
}

// StackRoleBindingCreate binds a stack to a role in a space.
func (s Server) StackRoleBindingCreate(
	ctx context.Context,
	req api.StackRoleBindingCreateRequestObject,
) (api.StackRoleBindingCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b, err := s.stacks.Bind(ctx, p, bindRequest(*req.Body))
	if err != nil {
		return nil, bindingError(err, req.Body.StackID)
	}
	return api.StackRoleBindingCreate201JSONResponse{Body: bindingView(b)}, nil
}

// StackRoleBindingBatchCreate binds each stack its input names, all or
// none.
func (s Server) StackRoleBindingBatchCreate(
	ctx context.Context,
	req api.StackRoleBindingBatchCreateRequestObject,
) (api.StackRoleBindingBatchCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	rs := make([]core.BindRequest, 0, len(req.Body.Bindings))
	for _, in := range req.Body.Bindings {
		rs = append(rs, bindRequest(in))
	}
	bs, err := s.stacks.BindAll(ctx, p, rs)
	if err != nil {
		return nil, bindingError(err, "")
	}
	out := make([]api.StackRoleBinding, 0, len(bs))
	for _, b := range bs {
		out = append(out, bindingView(b))
	}
	return api.StackRoleBindingBatchCreate201JSONResponse{Body: out}, nil
}

// StackRoleBindingUpdate moves a stack role binding to its input's role
// and space.
func (s Server) StackRoleBindingUpdate(
	ctx context.Context,
	req api.StackRoleBindingUpdateRequestObject,
) (api.StackRoleBindingUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b, err := s.stacks.Rebind(ctx, p, req.StackRoleBindingId, strict.IfMatch(req.Params.IfMatch),
		core.BindRequest{Role: req.Body.RoleID, Space: req.Body.SpaceID})
	if err != nil {
		return nil, bindingError(err, "")
	}
	return api.StackRoleBindingUpdate200JSONResponse{
		Body:    bindingView(b),
		Headers: api.StackRoleBindingUpdate200ResponseHeaders{ETag: bindingTag(b)},
	}, nil
}

// StackRoleBindingPatch changes the role or space its merge patch names
// and keeps the other, writing the result as StackRoleBindingUpdate does.
func (s Server) StackRoleBindingPatch(
	ctx context.Context,
	req api.StackRoleBindingPatchRequestObject,
) (api.StackRoleBindingPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.StackRoleBindingUpdateInput, int64, error) {
		b, err := s.stacks.Binding(ctx, p.AccountID, req.StackRoleBindingId)
		if err != nil {
			return api.StackRoleBindingUpdateInput{}, 0, err //nolint:wrapcheck // answered below
		}
		return api.StackRoleBindingUpdateInput{RoleID: b.Role, SpaceID: b.Space}, b.Version, nil
	}
	write := func(in api.StackRoleBindingUpdateInput, expect kernel.Expect) (core.Binding, error) {
		//nolint:wrapcheck // as above
		return s.stacks.Rebind(ctx, p, req.StackRoleBindingId, expect,
			core.BindRequest{Role: in.RoleID, Space: in.SpaceID})
	}
	b, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, bindingError(err, "")
	}
	return api.StackRoleBindingPatch200JSONResponse{
		Body:    bindingView(b),
		Headers: api.StackRoleBindingPatch200ResponseHeaders{ETag: bindingTag(b)},
	}, nil
}

// StackRoleBindingDelete deletes a stack role binding and answers it as
// it was.
func (s Server) StackRoleBindingDelete(
	ctx context.Context,
	req api.StackRoleBindingDeleteRequestObject,
) (api.StackRoleBindingDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b, err := s.stacks.Unbind(ctx, p, req.StackRoleBindingId, strict.IfMatch(req.Params.IfMatch))
	if err != nil {
		return nil, bindingError(err, "")
	}
	return api.StackRoleBindingDelete200JSONResponse{Body: bindingView(b)}, nil
}

// StackRoleBindings lists a stack's role bindings, by ID.
func (s Server) StackRoleBindings(
	ctx context.Context,
	req api.StackRoleBindingsRequestObject,
) (api.StackRoleBindingsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var after kernel.ULID
	if req.Params.Cursor != nil {
		c, err := page.Decode[string](bindingsList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		if after, err = kernel.ParseULID(c.ID); err != nil {
			//nolint:wrapcheck // as above
			return nil, problem.InvalidCursor.Error("The cursor's position is not of its list.")
		}
	}
	bs, err := s.stacks.Bindings(ctx, p.AccountID, req.StackId)
	if err != nil {
		return nil, stackError(err, req.StackId)
	}
	slices.SortFunc(bs, func(a, b core.Binding) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	if req.Params.Cursor != nil {
		bs = slices.DeleteFunc(bs, func(b core.Binding) bool {
			return bytes.Compare(b.ID[:], after[:]) <= 0
		})
	}
	out := api.StackRoleBindingPage{Items: []api.StackRoleBinding{}}
	if n := int(kernel.PageLimit(req.Params.Limit)); len(bs) > n {
		bs = bs[:n]
		last := bs[n-1].ID.String()
		next, err := page.Encode(bindingsList, kernel.Cursor[string]{Key: last, ID: last})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding a string cannot fail
		}
		out.NextCursor = &next
	}
	for _, b := range bs {
		out.Items = append(out.Items, bindingView(b))
	}
	return api.StackRoleBindings200JSONResponse{Body: out}, nil
}

// StacksWithAccessToSpace searches the stacks a space's access view
// lists in its mode, each with the grants that reach the space for it.
func (s Server) StacksWithAccessToSpace(
	ctx context.Context,
	req api.StacksWithAccessToSpaceRequestObject,
) (api.StacksWithAccessToSpaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.stacks.Holding(ctx, p, req.Params.Space, mode(req.Params.Mode),
		apisearch.Input(req.Params.Input))
	if err != nil {
		return nil, stackError(err, "")
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	out := api.StacksWithAccessToSpaceOutput{
		Edges:    make([]api.StackWithAccessToSpaceEdge, 0, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.StackWithAccessToSpaceEdge{
			Cursor: e.Cursor, Node: holdingView(e.Node, slugs),
		})
	}
	return api.StacksWithAccessToSpace200JSONResponse{Body: out}, nil
}

// StacksWithAccessToSpaceSuggestions lists the values the stacks a
// space's access view lists in its mode hold.
func (s Server) StacksWithAccessToSpaceSuggestions(
	ctx context.Context,
	req api.StacksWithAccessToSpaceSuggestionsRequestObject,
) (api.StacksWithAccessToSpaceSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.stacks.SuggestHolding(ctx, p, req.Params.Space, mode(req.Params.Mode),
		apisearch.SuggestionsInput(req.Params.Input))
	if err != nil {
		return nil, stackError(err, "")
	}
	return api.StacksWithAccessToSpaceSuggestions200JSONResponse{
		Body: apisearch.Suggestions(got),
	}, nil
}

// mode is the access view's mode a request names, RESIDING when it names
// none.
func mode(m *api.StackAccessMode) string {
	if m == nil {
		return core.Residing
	}
	return string(*m)
}

func bindRequest(in api.StackRoleBindingInput) core.BindRequest {
	return core.BindRequest{Stack: in.StackID, Role: in.RoleID, Space: in.SpaceID}
}

// bindingView is b as the API answers it: its role, space and stack by
// slug.
func bindingView(b core.Binding) api.StackRoleBinding {
	return api.StackRoleBinding{
		Id: b.ID.String(), Kind: api.StackRoleBindingKindStackRoleBinding,
		Role: api.RoleRef{Id: b.Role}, Space: api.SpaceRef{Id: b.Space},
		StackID: b.Stack.Slug, StackName: b.Stack.Name,
	}
}

func bindingTag(b core.Binding) *string {
	t := strict.ETag(b.Version)
	return &t
}

// holdingView is h as the access view answers it, naming each grant's
// space by its slug in slugs. A stack's implicit read has no binding.
func holdingView(h core.Holding, slugs map[kernel.ULID]string) api.StackWithAccessToSpaceNode {
	out := api.StackWithAccessToSpaceNode{
		Stack: api.SpaceAccessStack{
			Id: h.Slug, Name: h.Name, Labels: orEmpty(h.Labels), State: api.StackState(h.State),
		},
		Roles: make([]api.StackSpaceAccessRoleGrant, 0, len(h.Grants)),
	}
	for _, g := range h.Grants {
		v := api.StackSpaceAccessRoleGrant{
			Role: api.RoleRef{Id: g.Role}, Source: api.StackSpaceAccessGrantSource(g.Source),
			Space: api.SpaceRef{Id: slugs[g.Space]},
		}
		if !g.Binding.IsZero() {
			v.Binding = &api.StackRoleBindingRef{Id: g.Binding.String()}
		}
		out.Roles = append(out.Roles, v)
	}
	return out
}

// bindingError answers the service's refusals of a stack binding: 404 for
// a binding the account lacks, 409 already-exists for a binding it holds,
// and as stackError does otherwise, slug naming the stack.
func bindingError(err error, slug string) error {
	if errors.Is(err, accessapi.ErrNoBinding) {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error("No such stack role binding in this account.")
	}
	if errors.Is(err, accessapi.ErrBindingHeld) {
		//nolint:wrapcheck // as above
		return problem.AlreadyExists.Error(
			"The binding is one the account holds: " + err.Error() + ".",
		)
	}
	return stackError(err, slug)
}
