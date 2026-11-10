package http

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
)

// rolesList is the roles list's cursor name.
const rolesList = "roles"

// Roles lists the caller's account's roles, system and custom, by slug.
func (s Server) Roles(
	ctx context.Context,
	req api.RolesRequestObject,
) (api.RolesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	rs, next, err := listPage(rolesList, req.Params.Cursor, req.Params.Limit,
		func(id string) (string, error) { return id, nil },
		func(after string, n int) ([]role.Role, error) {
			return s.roles.Roles(
				ctx,
				p.AccountID,
				after,
				n,
			) //nolint:wrapcheck // the store names the read
		},
		func(r role.Role) string { return r.Slug })
	if err != nil {
		return nil, err
	}
	out := api.RolePage{Items: make([]api.Role, 0, len(rs)), NextCursor: next}
	for _, r := range rs {
		v, err := s.roleView(ctx, p.AccountID, r)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, v)
	}
	return api.Roles200JSONResponse{Body: out}, nil
}

// Role answers one role of the caller's account, tagged with its version.
func (s Server) Role(
	ctx context.Context,
	req api.RoleRequestObject,
) (api.RoleResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.roles.Role(ctx, p.AccountID, req.RoleId)
	if err != nil {
		return nil, roleError(err, req.RoleId)
	}
	v, err := s.roleView(ctx, p.AccountID, r)
	if err != nil {
		return nil, err
	}
	return api.Role200JSONResponse{
		Body: v, Headers: api.Role200ResponseHeaders{ETag: new(strict.ETag(r.Version))},
	}, nil
}

// RoleCreate creates a custom role, its slug made from its name.
func (s Server) RoleCreate(
	ctx context.Context,
	req api.RoleCreateRequestObject,
) (api.RoleCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := req.Body
	r, err := s.roles.Create(ctx, p.AccountID, role.Input{
		Name: in.Name, Description: in.Description, Actions: catalogued(in.Actions),
	})
	if err != nil {
		return nil, roleError(err, role.SlugOf(in.Name))
	}
	v, err := s.roleView(ctx, p.AccountID, r)
	if err != nil {
		return nil, err
	}
	return api.RoleCreate201JSONResponse{Body: v}, nil
}

// RoleUpdate changes the fields of a custom role its body names, when its
// If-Match, if any, names the role's version.
func (s Server) RoleUpdate(
	ctx context.Context,
	req api.RoleUpdateRequestObject,
) (api.RoleUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.updateRole(ctx, p.AccountID, req.RoleId, strict.IfMatch(req.Params.IfMatch),
		*req.Body)
	if err != nil {
		return nil, err
	}
	v, err := s.roleView(ctx, p.AccountID, r)
	if err != nil {
		return nil, err
	}
	return api.RoleUpdate200JSONResponse{
		Body: v, Headers: api.RoleUpdate200ResponseHeaders{ETag: new(strict.ETag(r.Version))},
	}, nil
}

// RolePatch changes the fields its merge patch names and keeps the rest,
// writing the result as RoleUpdate writes its body.
func (s Server) RolePatch(
	ctx context.Context,
	req api.RolePatchRequestObject,
) (api.RolePatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.RoleUpdateInput, int64, error) {
		r, err := s.roles.Role(ctx, p.AccountID, req.RoleId)
		if err != nil {
			return api.RoleUpdateInput{}, 0, roleError(err, req.RoleId)
		}
		return api.RoleUpdateInput{
			Name: &r.Name, Description: &r.Description, Actions: new(actions(r.Actions)),
		}, r.Version, nil
	}
	write := func(in api.RoleUpdateInput, expect kernel.Expect) (role.Role, error) {
		return s.updateRole(ctx, p.AccountID, req.RoleId, expect, in)
	}
	r, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // read and write answer their problems
	}
	v, err := s.roleView(ctx, p.AccountID, r)
	if err != nil {
		return nil, err
	}
	return api.RolePatch200JSONResponse{
		Body: v, Headers: api.RolePatch200ResponseHeaders{ETag: new(strict.ETag(r.Version))},
	}, nil
}

// updateRole writes in to acct's role slug, as RoleUpdate and RolePatch
// do, when expect accepts its version.
func (s Server) updateRole(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	expect kernel.Expect,
	in api.RoleUpdateInput,
) (role.Role, error) {
	c := role.Change{Name: in.Name, Description: in.Description}
	if in.Actions != nil {
		c.Actions = new(catalogued(*in.Actions))
	}
	r, err := s.roles.Update(ctx, acct, slug, expect, c)
	if err != nil {
		return role.Role{}, roleError(err, slug)
	}
	return r, nil
}

// RoleDelete deletes a custom role without bindings, when its If-Match, if
// any, names the role's version, and answers it as it was.
func (s Server) RoleDelete(
	ctx context.Context,
	req api.RoleDeleteRequestObject,
) (api.RoleDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.roles.Delete(ctx, p.AccountID, req.RoleId, strict.IfMatch(req.Params.IfMatch))
	if err != nil {
		return nil, roleError(err, req.RoleId)
	}
	v, err := s.roleView(ctx, p.AccountID, r)
	if err != nil {
		return nil, err
	}
	return api.RoleDelete200JSONResponse{Body: v}, nil
}

// roleView is r as Spacelift's Role, with its bindings to users, groups
// and API keys. Its count covers every binding, a stack's included.
func (s Server) roleView(
	ctx context.Context,
	acct kernel.AccountID,
	r role.Role,
) (api.Role, error) {
	bs, err := s.roles.Bindings(ctx, acct, r)
	if err != nil {
		return api.Role{}, err //nolint:wrapcheck // the store names the read
	}
	out := api.Role{
		Id: r.Slug, Slug: r.Slug, Name: r.Name, Description: r.Description, IsSystem: r.System,
		Actions:           actions(r.Actions),
		RoleBindingsCount: int32(min(r.Bindings, 1<<31-1)), //nolint:gosec // clamped
		RoleBindings:      make([]api.RoleBinding, 0, len(bs)),
	}
	for _, b := range bs {
		v, err := bindingView(r.Slug, b)
		if err != nil {
			return api.Role{}, err
		}
		out.RoleBindings = append(out.RoleBindings, v)
	}
	return out, nil
}

// bindingView is b of the role slug as its actor kind's binding.
func bindingView(slug string, b role.Listed) (api.RoleBinding, error) {
	var (
		v     api.RoleBinding
		err   error
		id    = b.ID.String()
		actor = b.ActorID.String()
		space = api.SpaceRef{Id: b.Space}
	)
	switch b.ActorKind {
	case role.ActorUser:
		err = v.FromUserRoleBinding(api.UserRoleBinding{
			Id: id, Kind: api.UserRoleBindingKindUserRoleBinding, Role: api.RoleRef{Id: slug},
			RoleID: slug, Space: space, User: api.ManagedUserRef{Id: actor}, UserID: actor,
		})
	case role.ActorGroup:
		err = v.FromUserGroupRoleBinding(api.UserGroupRoleBinding{
			Id: id, Kind: api.UserGroupRoleBindingKindUserGroupRoleBinding, RoleID: slug,
			Space: space, UserGroup: api.ManagedUserGroupRef{Id: actor},
		})
	default:
		err = v.FromApiKeyRoleBinding(api.ApiKeyRoleBinding{
			Id: id, Kind: api.ApiKeyRoleBindingKindApiKeyRoleBinding, Role: api.RoleRef{Id: slug},
			Space: space, ApiKey: api.ApiKeyRef{Id: actor}, ApiKeyID: actor,
		})
	}
	return v, err //nolint:wrapcheck // encoding a struct of strings cannot fail
}

// roleError is err as the problem it answers: 404 for a role the account
// does not hold, 409 already-exists for a slug in use, 409 invalid-state
// for a role with bindings, 422 for a system role's change, and err
// itself otherwise, a *kernel.Invalid answering 422 with its pointers.
func roleError(err error, slug string) error {
	switch {
	case errors.Is(err, port.ErrNotFound):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No role %q in this account.", slug))
	case errors.Is(err, port.ErrSlugTaken):
		//nolint:wrapcheck // as above
		return problem.AlreadyExists.Error(fmt.Sprintf("The account has a role %q.", slug))
	case errors.Is(err, role.ErrInUse):
		//nolint:wrapcheck // as above
		return problem.InvalidState.Error(fmt.Sprintf(
			"The role %q still has bindings; delete them first.", slug))
	case errors.Is(err, role.ErrSystem):
		//nolint:wrapcheck // as above
		return problem.ValidationFailed.Error(fmt.Sprintf(
			"The role %q is a system role, which never changes.", slug))
	}
	return err
}

// catalogued is as as the catalogue's actions, which the service checks.
func catalogued(as []api.Action) []accessapi.Action {
	out := make([]accessapi.Action, 0, len(as))
	for _, a := range as {
		out = append(out, accessapi.Action(a))
	}
	return out
}
