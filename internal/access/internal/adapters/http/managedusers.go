package http

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	managed "github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
)

// The managed user lists' cursor names.
const (
	managedUsersList     = "managed-users"
	managedGroupsList    = "managed-user-groups"
	groupUsersList       = "managed-user-group-users"
	availableGroupsList  = "available-user-groups"
	personalAPIKeysList  = "managed-user-personal-api-keys"
	identityProviderNone = api.IdentityProviderEMPTY
)

// managedError is err as the problem it answers: 403 for a role change no
// member asks for, 404
// for a user, group or space the account does not hold, 409 for a group
// name in use or a state the change refuses, 422 for input the service
// refuses, 403 limit-reached for a seat beyond the limit, and err itself
// otherwise.
func managedError(err error, what string) error {
	le, isLimit := errors.AsType[*featuresapi.LimitError](err)
	switch {
	case isLimit:
		return &problem.Error{
			Problem: problem.NewLimitReached(le.Limit, string(le.Scope), le.Value),
		}
	case errors.Is(err, managed.ErrNotMember):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.Forbidden.Error(err.Error())
	case errors.Is(err, port.ErrNotFound):
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf("No %s in this account.", what))
	case errors.Is(err, port.ErrSlugTaken):
		return problem.AlreadyExists.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, managed.ErrInvalidState):
		return problem.InvalidState.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, organization.ErrInvalid):
		return problem.ValidationFailed.Error(err.Error()) //nolint:wrapcheck // as above
	}
	return err
}

// userStatuses are the served statuses of a member's: an expired
// invitation is still PENDING, as Spacelift's UserStatus has no EXPIRED.
var userStatuses = map[member.Status]api.UserStatus{
	member.Active:      api.UserStatusACTIVE,
	member.Pending:     api.UserStatusPENDING,
	member.Deactivated: api.UserStatusINACTIVE,
}

// managedUser is u as Spacelift's ManagedUser, with acts as its
// allowedActions and the access rules among held, its bindings. role is
// USER; no identity provider is configured before P23's, so loginMethod
// is EMPTY.
func managedUser(u managed.User, acts []api.Action, held []binding.Listed) api.ManagedUser {
	out := api.ManagedUser{
		AccessRules:          rules(held),
		AllowedActions:       acts,
		GroupCount:           int32(min(u.GroupCount, 1<<31-1)), //nolint:gosec // clamped
		Id:                   u.Member.ID.String(),
		LatestGroups:         nonNil(u.LatestGroups),
		LoginMethod:          identityProviderNone,
		Role:                 api.UserRoleUSER,
		ScimGroupMemberships: []string{},
		Status:               userStatuses[u.Member.Status],
		Username:             u.Member.Login,
	}
	if u.SlackMemberID != "" {
		out.SlackMemberID = &u.SlackMemberID
	}
	if u.Member.LastSeenAt != nil {
		t := unix(*u.Member.LastSeenAt)
		out.LastLoginTime = &t
	}
	if inv := u.Invitation; inv != nil {
		sent := unix(inv.SentAt)
		out.LatestInvitation = &api.ManagedUserInvitation{
			CreatedAt: unix(inv.CreatedAt),
			SentAt:    &sent,
		}
		if inv.Email != "" {
			out.InvitationEmail = &inv.Email
		}
	}
	return out
}

// managedGroup is g as Spacelift's ManagedUserGroup, with the access
// rules among held, its bindings.
func managedGroup(g group.Group, held []binding.Listed) api.ManagedUserGroup {
	return api.ManagedUserGroup{
		AccessRules:      rules(held),
		Description:      g.Description,
		GroupName:        g.Name,
		Id:               g.ID.String(),
		IdentityProvider: identityProviderNone,
		MemberCount:      int32(min(len(g.Members), 1<<31-1)), //nolint:gosec // clamped
		Members:          nonNil(g.Members),
		Status:           api.UserGroupStatusACTIVE,
	}
}

// userViews are managedUser of each of us with the actions the caller
// holds on a managed user and its bindings.
func (s Server) userViews(
	ctx context.Context,
	p kernel.Principal,
	us []managed.User,
) ([]api.ManagedUser, error) {
	acts, err := s.actionsOn(ctx, p, entityManagedUser)
	if err != nil {
		return nil, err
	}
	out := make([]api.ManagedUser, 0, len(us))
	for _, u := range us {
		held, err := s.bindings.Held(ctx, p.AccountID, binding.ActorUser, u.Member.ID.ULID)
		if err != nil {
			return nil, err //nolint:wrapcheck // the store names the read
		}
		out = append(out, managedUser(u, acts, held))
	}
	return out, nil
}

// userView is userViews of u alone.
func (s Server) userView(
	ctx context.Context,
	p kernel.Principal,
	u managed.User,
) (api.ManagedUser, error) {
	vs, err := s.userViews(ctx, p, []managed.User{u})
	if err != nil {
		return api.ManagedUser{}, err
	}
	return vs[0], nil
}

// groupView is managedGroup of g with its bindings.
func (s Server) groupView(
	ctx context.Context,
	p kernel.Principal,
	g group.Group,
) (api.ManagedUserGroup, error) {
	held, err := s.bindings.Held(ctx, p.AccountID, binding.ActorGroup, g.ID)
	if err != nil {
		return api.ManagedUserGroup{}, err //nolint:wrapcheck // the store names the read
	}
	return managedGroup(g, held), nil
}

func userID(u managed.User) string { return u.Member.ID.String() }

func parseULID(s string) (kernel.ULID, error) {
	return kernel.ParseULID(s) //nolint:wrapcheck // answered as an invalid cursor
}

// ManagedUsers lists the account's managed users, oldest member first.
func (s Server) ManagedUsers(
	ctx context.Context,
	req api.ManagedUsersRequestObject,
) (api.ManagedUsersResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	us, next, err := listPage(managedUsersList, req.Params.Cursor, req.Params.Limit, parseMember,
		func(after kernel.MemberID, n int) ([]managed.User, error) {
			return s.users.Users(ctx, p, after, n)
		}, userID)
	if err != nil {
		return nil, err
	}
	items, err := s.userViews(ctx, p, us)
	if err != nil {
		return nil, err
	}
	return api.ManagedUsers200JSONResponse{
		Body: api.ManagedUserPage{Items: items, NextCursor: next},
	}, nil
}

// ManagedUser reads one of the account's managed users, tagged with its
// version.
func (s Server) ManagedUser(
	ctx context.Context,
	req api.ManagedUserRequestObject,
) (api.ManagedUserResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.users.User(ctx, p, req.ManagedUserId)
	if err != nil {
		return nil, managedError(err, "managed user")
	}
	v, err := s.userView(ctx, p, u)
	if err != nil {
		return nil, err
	}
	return api.ManagedUser200JSONResponse{
		Body: v, Headers: api.ManagedUser200ResponseHeaders{ETag: new(strict.ETag(u.Version))},
	}, nil
}

// ManagedUserInviteV2 invites a person to the account with the role
// bindings the invitation names, all or none.
func (s Server) ManagedUserInviteV2(
	ctx context.Context,
	req api.ManagedUserInviteV2RequestObject,
) (api.ManagedUserInviteV2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	bs, err := s.prepare(
		ctx,
		p,
		binding.ActorUser,
		legacy{bindings: roleRequests(req.Body.RoleBindings)},
	)
	if err != nil {
		return nil, err
	}
	var u managed.User
	err = s.atomic(ctx, func(ctx context.Context) error {
		if u, err = s.users.Invite(ctx, p, from(ctx).addr, managed.Invite{
			Login:         req.Body.Username,
			Email:         deref(req.Body.InvitationEmail),
			SlackMemberID: deref(req.Body.SlackMemberID),
		}); err != nil {
			return managedError(err, "managed user")
		}
		return bindingError(
			s.bind(ctx, p.AccountID, binding.ActorUser, u.Member.ID.ULID, bs),
			"",
			"",
		)
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // each write in the transaction answers its own problem
	}
	v, err := s.userView(ctx, p, u)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserInviteV2201JSONResponse{Body: v}, nil
}

// ManagedUserUpdate sets a managed user's Slack member, when its
// If-Match, if any, names the managed user's version.
func (s Server) ManagedUserUpdate(
	ctx context.Context,
	req api.ManagedUserUpdateRequestObject,
) (api.ManagedUserUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.updateUser(ctx, p, req.ManagedUserId, strict.IfMatch(req.Params.IfMatch),
		*req.Body)
	if err != nil {
		return nil, err
	}
	v, err := s.userView(ctx, p, u)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserUpdate200JSONResponse{
		Body:    v,
		Headers: api.ManagedUserUpdate200ResponseHeaders{ETag: new(strict.ETag(u.Version))},
	}, nil
}

// ManagedUserPatch changes the members its merge patch names and keeps
// the rest, writing the result as ManagedUserUpdate writes its body.
func (s Server) ManagedUserPatch(
	ctx context.Context,
	req api.ManagedUserPatchRequestObject,
) (api.ManagedUserPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.ManagedUserUpdateInputBody, int64, error) {
		u, err := s.users.User(ctx, p, req.ManagedUserId)
		if err != nil {
			return api.ManagedUserUpdateInputBody{}, 0, managedError(err, "managed user")
		}
		held, err := s.bindings.Held(ctx, p.AccountID, binding.ActorUser, u.Member.ID.ULID)
		if err != nil {
			return api.ManagedUserUpdateInputBody{}, 0, err //nolint:wrapcheck // the store names the read
		}
		return userInput(u, held), u.Version, nil
	}
	write := func(in api.ManagedUserUpdateInputBody, expect kernel.Expect) (managed.User, error) {
		return s.updateUser(ctx, p, req.ManagedUserId, expect, in)
	}
	u, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // read and write answer their problems
	}
	v, err := s.userView(ctx, p, u)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserPatch200JSONResponse{
		Body: v, Headers: api.ManagedUserPatch200ResponseHeaders{ETag: new(strict.ETag(u.Version))},
	}, nil
}

// updateUser writes in to the managed user id, as ManagedUserUpdate and
// ManagedUserPatch do, when expect accepts its version: its Slack member,
// and its access rules in place of those it holds, all or none.
func (s Server) updateUser(
	ctx context.Context,
	p kernel.Principal,
	id string,
	expect kernel.Expect,
	in api.ManagedUserUpdateInputBody,
) (managed.User, error) {
	cur, err := s.users.User(ctx, p, id)
	if err != nil {
		return managed.User{}, managedError(err, "managed user")
	}
	actor := cur.Member.ID.ULID
	bs, err := s.prepare(ctx, p, binding.ActorUser, legacy{rules: ruleRequests(in.AccessRules)})
	if err == nil {
		err = s.bindings.Releasable(ctx, p, binding.ActorUser, actor, "/accessRules", bs.rules)
	}
	if err != nil {
		return managed.User{}, err //nolint:wrapcheck // a refusal, answered as it is
	}
	var u managed.User
	err = s.atomic(ctx, func(ctx context.Context) error {
		if u, err = s.users.Update(ctx, p, from(ctx).addr, id, expect,
			deref(in.SlackMemberID)); err != nil {
			return managedError(err, "managed user")
		}
		return bindingError(
			s.bindings.ReplaceRules(ctx, p.AccountID, binding.ActorUser, actor, bs.rules),
			"",
			"",
		)
	})
	return u, err //nolint:wrapcheck // each write in the transaction answers its own problem
}

// userInput is u as the ManagedUserUpdateInputBody that writes it: the
// access rules among held, its bindings, and its Slack member, if any.
func userInput(u managed.User, held []binding.Listed) api.ManagedUserUpdateInputBody {
	in := api.ManagedUserUpdateInputBody{AccessRules: ruleInputs(rules(held))}
	if u.SlackMemberID != "" {
		in.SlackMemberID = &u.SlackMemberID
	}
	return in
}

// ManagedUserDelete removes a managed user from the account, when its
// If-Match, if any, names the managed user's version, and answers them as
// they were.
func (s Server) ManagedUserDelete(
	ctx context.Context,
	req api.ManagedUserDeleteRequestObject,
) (api.ManagedUserDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.users.Delete(ctx, p, from(ctx).addr, req.ManagedUserId,
		strict.IfMatch(req.Params.IfMatch))
	if err != nil {
		return nil, managedError(err, "managed user")
	}
	v, err := s.userView(ctx, p, u)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserDelete200JSONResponse{Body: v}, nil
}

// ManagedUserResendInvite restarts a pending managed user's invitation.
func (s Server) ManagedUserResendInvite(
	ctx context.Context,
	req api.ManagedUserResendInviteRequestObject,
) (api.ManagedUserResendInviteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.users.Resend(ctx, p, from(ctx).addr, req.ManagedUserId)
	if err != nil {
		return nil, managedError(err, "managed user")
	}
	v, err := s.userView(ctx, p, u)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserResendInvite200JSONResponse{Body: v}, nil
}

// ManagedUserRequestInvite records a request that the account's admins
// invite a person.
func (s Server) ManagedUserRequestInvite(
	ctx context.Context,
	req api.ManagedUserRequestInviteRequestObject,
) (api.ManagedUserRequestInviteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.users.RequestInvite(ctx, p, from(ctx).addr, req.Body.Email,
		req.Body.UserName); err != nil {
		return nil, managedError(err, "managed user")
	}
	return api.ManagedUserRequestInvite201JSONResponse{Body: true}, nil
}

// ManagedUserRequestRoleChange records the caller's request for an access
// level on a space.
func (s Server) ManagedUserRequestRoleChange(
	ctx context.Context,
	req api.ManagedUserRequestRoleChangeRequestObject,
) (api.ManagedUserRequestRoleChangeResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.users.RequestRoleChange(ctx, p, from(ctx).addr, req.SpaceId,
		string(req.Body.RequestedAccessLevel)); err != nil {
		return nil, managedError(err, "space")
	}
	return api.ManagedUserRequestRoleChange201JSONResponse{Body: true}, nil
}

// ManagedUserPersonalApiKeys lists a managed user's personal API keys in
// the account, oldest first.
//
//nolint:revive // the generated interface names it
func (s Server) ManagedUserPersonalApiKeys(
	ctx context.Context,
	req api.ManagedUserPersonalApiKeysRequestObject,
) (api.ManagedUserPersonalApiKeysResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	keys, next, err := listPage(personalAPIKeysList, req.Params.Cursor, req.Params.Limit, parseULID,
		func(after kernel.ULID, n int) ([]apikey.Key, error) {
			return s.users.PersonalAPIKeys(ctx, p, req.ManagedUserId, after, n)
		}, func(k apikey.Key) string { return k.ID.String() })
	if err != nil {
		return nil, managedError(err, "managed user")
	}
	acts, err := s.keyActions(ctx)
	if err != nil {
		return nil, err
	}
	body := api.ApiKeyPage{Items: make([]api.ApiKey, 0, len(keys)), NextCursor: next}
	for _, k := range keys {
		v, err := s.keyOf(ctx, k, "", acts)
		if err != nil {
			return nil, err
		}
		body.Items = append(body.Items, v)
	}
	return api.ManagedUserPersonalApiKeys200JSONResponse{Body: body}, nil
}

// ManagedUserGroups lists the account's managed user groups, oldest first.
func (s Server) ManagedUserGroups(
	ctx context.Context,
	req api.ManagedUserGroupsRequestObject,
) (api.ManagedUserGroupsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	gs, next, err := listPage(managedGroupsList, req.Params.Cursor, req.Params.Limit, parseULID,
		func(after kernel.ULID, n int) ([]group.Group, error) {
			return s.groups.Groups(ctx, p, after, n)
		}, func(g group.Group) string { return g.ID.String() })
	if err != nil {
		return nil, err
	}
	body := api.ManagedUserGroupPage{
		Items:      make([]api.ManagedUserGroup, 0, len(gs)),
		NextCursor: next,
	}
	for _, g := range gs {
		v, err := s.groupView(ctx, p, g)
		if err != nil {
			return nil, err
		}
		body.Items = append(body.Items, v)
	}
	return api.ManagedUserGroups200JSONResponse{Body: body}, nil
}

// ManagedUserGroup reads one of the account's managed user groups, tagged
// with its version.
func (s Server) ManagedUserGroup(
	ctx context.Context,
	req api.ManagedUserGroupRequestObject,
) (api.ManagedUserGroupResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	g, err := s.groups.Group(ctx, p, req.ManagedUserGroupId)
	if err != nil {
		return nil, managedError(err, "managed user group")
	}
	v, err := s.groupView(ctx, p, g)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserGroup200JSONResponse{
		Body:    v,
		Headers: api.ManagedUserGroup200ResponseHeaders{ETag: new(strict.ETag(g.Version))},
	}, nil
}

// ManagedUserGroupCreateV2 creates a managed user group of the account
// with the role bindings its input names, all or none.
func (s Server) ManagedUserGroupCreateV2(
	ctx context.Context,
	req api.ManagedUserGroupCreateV2RequestObject,
) (api.ManagedUserGroupCreateV2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	bs, err := s.prepare(
		ctx,
		p,
		binding.ActorGroup,
		legacy{bindings: roleRequests(req.Body.RoleBindings)},
	)
	if err != nil {
		return nil, err
	}
	var g group.Group
	err = s.atomic(ctx, func(ctx context.Context) error {
		if g, err = s.groups.Create(ctx, p, from(ctx).addr, req.Body.GroupName,
			deref(req.Body.Description)); err != nil {
			return managedError(err, "managed user group")
		}
		return bindingError(s.bind(ctx, p.AccountID, binding.ActorGroup, g.ID, bs), "", "")
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // each write in the transaction answers its own problem
	}
	v, err := s.groupView(ctx, p, g)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserGroupCreateV2201JSONResponse{Body: v}, nil
}

// ManagedUserGroupUpdateV2 sets a managed user group's description, when
// its If-Match, if any, names the group's version; none clears it.
func (s Server) ManagedUserGroupUpdateV2(
	ctx context.Context,
	req api.ManagedUserGroupUpdateV2RequestObject,
) (api.ManagedUserGroupUpdateV2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	g, err := s.updateGroup(ctx, p, req.ManagedUserGroupId, strict.IfMatch(req.Params.IfMatch),
		*req.Body)
	if err != nil {
		return nil, err
	}
	v, err := s.groupView(ctx, p, g)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserGroupUpdateV2200JSONResponse{
		Body:    v,
		Headers: api.ManagedUserGroupUpdateV2200ResponseHeaders{ETag: new(strict.ETag(g.Version))},
	}, nil
}

// ManagedUserGroupPatch changes the members its merge patch names and
// keeps the rest, writing the result as ManagedUserGroupUpdateV2 writes
// its body.
func (s Server) ManagedUserGroupPatch(
	ctx context.Context,
	req api.ManagedUserGroupPatchRequestObject,
) (api.ManagedUserGroupPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.ManagedUserGroupUpdateInputV2Body, int64, error) {
		g, err := s.groups.Group(ctx, p, req.ManagedUserGroupId)
		if err != nil {
			return api.ManagedUserGroupUpdateInputV2Body{}, 0,
				managedError(err, "managed user group")
		}
		return api.ManagedUserGroupUpdateInputV2Body{Description: &g.Description}, g.Version, nil
	}
	write := func(
		in api.ManagedUserGroupUpdateInputV2Body,
		expect kernel.Expect,
	) (group.Group, error) {
		return s.updateGroup(ctx, p, req.ManagedUserGroupId, expect, in)
	}
	g, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // read and write answer their problems
	}
	v, err := s.groupView(ctx, p, g)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserGroupPatch200JSONResponse{
		Body:    v,
		Headers: api.ManagedUserGroupPatch200ResponseHeaders{ETag: new(strict.ETag(g.Version))},
	}, nil
}

// updateGroup writes in to the managed user group id, as
// ManagedUserGroupUpdateV2 and ManagedUserGroupPatch do, when expect
// accepts its version.
func (s Server) updateGroup(
	ctx context.Context,
	p kernel.Principal,
	id string,
	expect kernel.Expect,
	in api.ManagedUserGroupUpdateInputV2Body,
) (group.Group, error) {
	g, err := s.groups.Update(ctx, p, from(ctx).addr, id, expect, deref(in.Description))
	if err != nil {
		return group.Group{}, managedError(err, "managed user group")
	}
	return g, nil
}

// ManagedUserGroupDelete deletes a managed user group, when its If-Match,
// if any, names the group's version, and answers it as it was.
func (s Server) ManagedUserGroupDelete(
	ctx context.Context,
	req api.ManagedUserGroupDeleteRequestObject,
) (api.ManagedUserGroupDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	g, err := s.groups.Delete(ctx, p, from(ctx).addr, req.ManagedUserGroupId,
		strict.IfMatch(req.Params.IfMatch))
	if err != nil {
		return nil, managedError(err, "managed user group")
	}
	return api.ManagedUserGroupDelete200JSONResponse{Body: managedGroup(g, nil)}, nil
}

// ManagedUserGroupManagedUsers lists a managed user group's members.
func (s Server) ManagedUserGroupManagedUsers(
	ctx context.Context,
	req api.ManagedUserGroupManagedUsersRequestObject,
) (api.ManagedUserGroupManagedUsersResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	us, next, err := listPage(groupUsersList, req.Params.Cursor, req.Params.Limit, parseMember,
		func(after kernel.MemberID, n int) ([]managed.User, error) {
			return s.groups.Users(ctx, p, req.ManagedUserGroupId, after, n)
		}, userID)
	if err != nil {
		return nil, managedError(err, "managed user group")
	}
	items, err := s.userViews(ctx, p, us)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserGroupManagedUsers200JSONResponse{
		Body: api.ManagedUserPage{Items: items, NextCursor: next},
	}, nil
}

// AvailableUserGroups lists the group names the account's managed users'
// latest sign-ins reported.
func (s Server) AvailableUserGroups(
	ctx context.Context,
	req api.AvailableUserGroupsRequestObject,
) (api.AvailableUserGroupsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	names, next, err := listPage(availableGroupsList, req.Params.Cursor, req.Params.Limit,
		func(s string) (string, error) { return s, nil },
		func(after string, n int) ([]string, error) {
			return s.groups.Available(ctx, p, after, n)
		}, func(n string) string { return n })
	if err != nil {
		return nil, err
	}
	return api.AvailableUserGroups200JSONResponse{
		Body: api.AvailableUserGroupsPage{Items: nonNil(names), NextCursor: next},
	}, nil
}
