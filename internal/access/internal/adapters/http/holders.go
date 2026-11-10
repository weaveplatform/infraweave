package http

import (
	"context"
	"math"
	"slices"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/holder"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
)

// held is a page of list's holders of the caller's space slug, and the
// slugs of its account's spaces, which the grants name. The route table
// has answered 404 or 403 for a space the caller cannot read or does not
// administer.
func (s Server) held(
	ctx context.Context,
	slug string,
	list holder.List,
	in api.SearchInput,
) (search.Result[holder.Held], map[kernel.ULID]string, error) {
	var out search.Result[holder.Held]
	p, err := principal(ctx)
	if err != nil {
		return out, nil, err
	}
	sp, err := s.spaces.Space(ctx, p.AccountID, slug)
	if err != nil {
		return out, nil, spaceError(err, slug)
	}
	out, err = s.holders.Search(ctx, p.AccountID, sp.ID, list, apisearch.Input(in))
	if err != nil {
		return out, nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return out, nil, err //nolint:wrapcheck // as above
	}
	return out, slugs, nil
}

// suggestHeld lists the values list's holders of the caller's space slug
// hold.
func (s Server) suggestHeld(
	ctx context.Context,
	slug string,
	list holder.List,
	in api.SearchSuggestionsInput,
) (api.SearchSuggestionsOutput, error) {
	p, err := principal(ctx)
	if err != nil {
		return api.SearchSuggestionsOutput{}, err
	}
	sp, err := s.spaces.Space(ctx, p.AccountID, slug)
	if err != nil {
		return api.SearchSuggestionsOutput{}, spaceError(err, slug)
	}
	got, err := s.holders.Suggest(ctx, p.AccountID, sp.ID, list, apisearch.SuggestionsInput(in))
	if err != nil {
		//nolint:wrapcheck // the store names the read; a refusal is answered as it is
		return api.SearchSuggestionsOutput{}, err
	}
	return apisearch.Suggestions(got), nil
}

// sourceOf is how g reaches the queried space, and whether it propagates
// from an ancestor.
func sourceOf(g authz.Grant) (api.StackSpaceAccessGrantSource, bool) {
	return api.StackSpaceAccessGrantSource(g.Source), g.Source == authz.Propagated
}

// sessionRoles are the roles of grants, each once, by how their bindings
// came to be.
func sessionRoles(grants []authz.Grant) []api.SessionSpaceAccessRoleGrant {
	out := []api.SessionSpaceAccessRoleGrant{}
	for _, g := range grants {
		r := api.SessionSpaceAccessRoleGrant{
			Origin: api.ASSIGNEDROLE,
			Role:   api.RoleRef{Id: g.Role},
		}
		if g.Origin == binding.OriginAccessRule {
			r.Origin = api.ACCESSLEVEL
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// count is n as the API's int32, which no account's sessions reach.
func count(n int) int32 {
	return int32(min(n, math.MaxInt32)) //nolint:gosec // clamped
}

// UsersWithAccessToSpace lists the managed users whose own bindings, or
// those of a group their latest sign-in reported, reach the space.
func (s Server) UsersWithAccessToSpace(
	ctx context.Context,
	req api.UsersWithAccessToSpaceRequestObject,
) (api.UsersWithAccessToSpaceResponseObject, error) {
	res, slugs, err := s.held(ctx, req.Params.Space, holder.Users, req.Params.Input)
	if err != nil {
		return nil, err
	}
	out := api.UsersWithAccessToSpaceOutput{
		Edges: []api.UsersWithAccessToSpaceEdge{}, PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		id := e.Node.ID.String()
		node := api.UsersWithAccessToSpaceNode{
			User: api.ManagedUserRef{Id: id}, Roles: []api.UserSpaceAccessRoleGrant{},
		}
		for _, g := range e.Node.Grants {
			src, propagated := sourceOf(g)
			r := api.UserSpaceAccessRoleGrant{
				BindingID: g.Binding.String(), Propagated: propagated, Source: src,
				Role: api.RoleRef{Id: g.Role}, Space: api.SpaceRef{Id: slugs[g.Space]},
			}
			if g.Actor == binding.ActorGroup {
				gr := e.Node.Groups[g.ActorID]
				err = r.GrantedVia.FromSpaceAccessGroup(api.SpaceAccessGroup{
					Id: g.ActorID.String(), GroupName: gr.Name, Description: gr.Description,
					Kind: api.SpaceAccessGroupKindSpaceAccessGroup,
				})
			} else {
				err = r.GrantedVia.FromUserRoleBinding(userBinding(binding.Listed{
					Binding: binding.Binding{ID: g.Binding, ActorID: e.Node.ID},
					Role:    g.Role, Space: slugs[g.Space],
				}))
			}
			if err != nil {
				return nil, err //nolint:wrapcheck // encoding a union member cannot fail
			}
			node.Roles = append(node.Roles, r)
		}
		out.Edges = append(out.Edges, api.UsersWithAccessToSpaceEdge{Cursor: e.Cursor, Node: node})
	}
	return api.UsersWithAccessToSpace200JSONResponse{Body: out}, nil
}

// UsersWithAccessToSpaceSuggestions lists the values of the users
// UsersWithAccessToSpace lists.
func (s Server) UsersWithAccessToSpaceSuggestions(
	ctx context.Context,
	req api.UsersWithAccessToSpaceSuggestionsRequestObject,
) (api.UsersWithAccessToSpaceSuggestionsResponseObject, error) {
	out, err := s.suggestHeld(ctx, req.Params.Space, holder.Users, req.Params.Input)
	if err != nil {
		return nil, err
	}
	return api.UsersWithAccessToSpaceSuggestions200JSONResponse{Body: out}, nil
}

// UserGroupsWithAccessToSpace lists the managed user groups whose
// bindings reach the space.
func (s Server) UserGroupsWithAccessToSpace(
	ctx context.Context,
	req api.UserGroupsWithAccessToSpaceRequestObject,
) (api.UserGroupsWithAccessToSpaceResponseObject, error) {
	res, slugs, err := s.held(ctx, req.Params.Space, holder.Groups, req.Params.Input)
	if err != nil {
		return nil, err
	}
	out := api.UserGroupsWithAccessToSpaceOutput{
		Edges: []api.UserGroupWithAccessToSpaceEdge{}, PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		node := api.UserGroupWithAccessToSpaceNode{
			UserGroup: api.ManagedUserGroupRef{Id: e.Node.ID.String()},
			Roles:     []api.UserGroupSpaceAccessRoleGrant{},
		}
		for _, g := range e.Node.Grants {
			src, propagated := sourceOf(g)
			node.Roles = append(node.Roles, api.UserGroupSpaceAccessRoleGrant{
				BindingID: g.Binding.String(), Propagated: propagated, Source: src,
				Role: api.RoleRef{Id: g.Role}, Space: api.SpaceRef{Id: slugs[g.Space]},
			})
		}
		out.Edges = append(
			out.Edges,
			api.UserGroupWithAccessToSpaceEdge{Cursor: e.Cursor, Node: node},
		)
	}
	return api.UserGroupsWithAccessToSpace200JSONResponse{Body: out}, nil
}

// UserGroupsWithAccessToSpaceSuggestions lists the values of the groups
// UserGroupsWithAccessToSpace lists.
func (s Server) UserGroupsWithAccessToSpaceSuggestions(
	ctx context.Context,
	req api.UserGroupsWithAccessToSpaceSuggestionsRequestObject,
) (api.UserGroupsWithAccessToSpaceSuggestionsResponseObject, error) {
	out, err := s.suggestHeld(ctx, req.Params.Space, holder.Groups, req.Params.Input)
	if err != nil {
		return nil, err
	}
	return api.UserGroupsWithAccessToSpaceSuggestions200JSONResponse{Body: out}, nil
}

// ApiKeysWithAccessToSpace lists the API keys whose own bindings, or
// those of the groups their teams name, reach the space.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeysWithAccessToSpace(
	ctx context.Context,
	req api.ApiKeysWithAccessToSpaceRequestObject,
) (api.ApiKeysWithAccessToSpaceResponseObject, error) {
	res, slugs, err := s.held(ctx, req.Params.Space, holder.Keys, req.Params.Input)
	if err != nil {
		return nil, err
	}
	out := api.ApiKeysWithAccessToSpaceOutput{
		Edges: []api.ApiKeyWithAccessToSpaceEdge{}, PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		node := api.ApiKeyWithAccessToSpaceNode{
			ApiKey: api.ApiKeyRef{
				Id: e.Node.ID.String(),
			},
			Roles: []api.ApiKeySpaceAccessRoleGrant{},
		}
		for _, g := range e.Node.Grants {
			src, propagated := sourceOf(g)
			node.Roles = append(node.Roles, api.ApiKeySpaceAccessRoleGrant{
				BindingID: g.Binding.String(), Propagated: propagated, Source: src,
				Role: api.RoleRef{Id: g.Role}, Space: api.SpaceRef{Id: slugs[g.Space]},
			})
		}
		out.Edges = append(out.Edges, api.ApiKeyWithAccessToSpaceEdge{Cursor: e.Cursor, Node: node})
	}
	return api.ApiKeysWithAccessToSpace200JSONResponse{Body: out}, nil
}

// ApiKeysWithAccessToSpaceSuggestions lists the values of the keys
// ApiKeysWithAccessToSpace lists.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeysWithAccessToSpaceSuggestions(
	ctx context.Context,
	req api.ApiKeysWithAccessToSpaceSuggestionsRequestObject,
) (api.ApiKeysWithAccessToSpaceSuggestionsResponseObject, error) {
	out, err := s.suggestHeld(ctx, req.Params.Space, holder.Keys, req.Params.Input)
	if err != nil {
		return nil, err
	}
	return api.ApiKeysWithAccessToSpaceSuggestions200JSONResponse{Body: out}, nil
}

// UserSessionsWithAccessToSpace lists the managed users with live
// sessions that reach the space, with the roles those sessions hold there.
func (s Server) UserSessionsWithAccessToSpace(
	ctx context.Context,
	req api.UserSessionsWithAccessToSpaceRequestObject,
) (api.UserSessionsWithAccessToSpaceResponseObject, error) {
	res, _, err := s.held(ctx, req.Params.Space, holder.UserSessions, req.Params.Input)
	if err != nil {
		return nil, err
	}
	out := api.UserSessionsWithAccessToSpaceOutput{
		Edges: []api.UserSessionsWithAccessToSpaceEdge{}, PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.UserSessionsWithAccessToSpaceEdge{
			Cursor: e.Cursor,
			Node: api.UserSessionsWithAccessToSpaceNode{
				User: api.SpaceAccessSessionUser{
					Login: e.Node.Login, Name: e.Node.Name, Email: e.Node.Email,
					LastLoginAt:  count(int(e.Node.Last.Unix())),
					SessionCount: count(e.Node.Sessions),
				},
				Roles: sessionRoles(e.Node.Grants),
			},
		})
	}
	return api.UserSessionsWithAccessToSpace200JSONResponse{Body: out}, nil
}

// UserSessionsWithAccessToSpaceSuggestions lists the values of the users
// UserSessionsWithAccessToSpace lists.
func (s Server) UserSessionsWithAccessToSpaceSuggestions(
	ctx context.Context,
	req api.UserSessionsWithAccessToSpaceSuggestionsRequestObject,
) (api.UserSessionsWithAccessToSpaceSuggestionsResponseObject, error) {
	out, err := s.suggestHeld(ctx, req.Params.Space, holder.UserSessions, req.Params.Input)
	if err != nil {
		return nil, err
	}
	return api.UserSessionsWithAccessToSpaceSuggestions200JSONResponse{Body: out}, nil
}

// ApiKeySessionsWithAccessToSpace lists the API keys with live sessions
// that reach the space, with the roles those sessions hold there.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeySessionsWithAccessToSpace(
	ctx context.Context,
	req api.ApiKeySessionsWithAccessToSpaceRequestObject,
) (api.ApiKeySessionsWithAccessToSpaceResponseObject, error) {
	res, _, err := s.held(ctx, req.Params.Space, holder.KeySessions, req.Params.Input)
	if err != nil {
		return nil, err
	}
	out := api.ApiKeySessionsWithAccessToSpaceOutput{
		Edges: []api.ApiKeySessionsWithAccessToSpaceEdge{}, PageInfo: apisearch.PageInfo(res),
	}
	for _, e := range res.Edges {
		out.Edges = append(out.Edges, api.ApiKeySessionsWithAccessToSpaceEdge{
			Cursor: e.Cursor,
			Node: api.ApiKeySessionsWithAccessToSpaceNode{
				ApiKey:       api.ApiKeyRef{Id: e.Node.ID.String()},
				SessionCount: count(e.Node.Sessions),
				Roles:        sessionRoles(e.Node.Grants),
			},
		})
	}
	return api.ApiKeySessionsWithAccessToSpace200JSONResponse{Body: out}, nil
}

// ApiKeySessionsWithAccessToSpaceSuggestions lists the values of the keys
// ApiKeySessionsWithAccessToSpace lists.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeySessionsWithAccessToSpaceSuggestions(
	ctx context.Context,
	req api.ApiKeySessionsWithAccessToSpaceSuggestionsRequestObject,
) (api.ApiKeySessionsWithAccessToSpaceSuggestionsResponseObject, error) {
	out, err := s.suggestHeld(ctx, req.Params.Space, holder.KeySessions, req.Params.Input)
	if err != nil {
		return nil, err
	}
	return api.ApiKeySessionsWithAccessToSpaceSuggestions200JSONResponse{Body: out}, nil
}
