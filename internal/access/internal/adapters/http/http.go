package http

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	managed "github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// Server is the access domain's partial of the API's strict server.
// internal/app embeds it beside the other domains' partials, so each
// operation it implements shadows the router's 501, and an operation two
// domains implement fails to compile.
type Server struct {
	auth     *authn.Service
	keys     *apikey.Service
	authz    accessapi.Authorizer
	spaces   Spaces
	accounts Accounts
	settings *settings.Service
	orgs     *orgadmin.Service
	users    *managed.Service
	groups   *group.Service
	oauth    *oauth.Service
}

// Accounts reads an account; port.Tenancy is one.
type Accounts interface {
	Account(ctx context.Context, id kernel.AccountID) (account.Account, error)
}

// Spaces lists an account's spaces, top first; space.Service is one.
type Spaces interface {
	Spaces(ctx context.Context, acct kernel.AccountID) ([]space.Space, error)
}

// Services are what the partial serves through: Orgs the Organization
// tag, Users and Groups the Managed Users tag, OAuth the consent
// operations of the authorization server, and Authz and Spaces the
// actions each view lists.
type Services struct {
	Auth     *authn.Service
	Keys     *apikey.Service
	Authz    accessapi.Authorizer
	Spaces   Spaces
	Accounts Accounts
	Settings *settings.Service
	Orgs     *orgadmin.Service
	Users    *managed.Service
	Groups   *group.Service
	OAuth    *oauth.Service
}

// NewServer returns the partial serving through sv.
func NewServer(sv Services) Server {
	return Server{
		auth: sv.Auth, keys: sv.Keys, authz: sv.Authz, spaces: sv.Spaces, accounts: sv.Accounts,
		settings: sv.Settings, orgs: sv.Orgs,
		users: sv.Users, groups: sv.Groups, oauth: sv.OAuth,
	}
}

// refused is the detail of every refused credential, which names no part.
const refused = "The request carries no valid credential."

// sessionsList names the sessions list's cursors.
const sessionsList = "sessions"

// ApiKeyUser exchanges an API key for an API token
// (POST /auth/token). It answers the reference's User, whose jwt and
// validUntil are the token and its expiry.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyUser(
	ctx context.Context,
	req api.ApiKeyUserRequestObject,
) (api.ApiKeyUserResponseObject, error) {
	g, err := s.auth.Exchange(ctx, req.Body.Id, req.Body.Secret, from(ctx).addr)
	if errors.Is(err, authn.ErrUnauthenticated) {
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.Unauthenticated.Error(refused)
	}
	// The key's first use in the seat window would take the organization
	// or account past its seats. The exchange checks the secret first, so
	// only a holder of the key learns the limit.
	if le, ok := errors.AsType[*featuresapi.LimitError](err); ok {
		return nil, &problem.Error{
			Problem: problem.NewLimitReached(le.Limit, string(le.Scope), le.Value),
		}
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	u, err := s.userOf(ctx, g.Session.Principal(), g.Token, g.Claims.Expiry)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyUser200JSONResponse{Body: u}, nil
}

// Viewer is the caller, with the token it presented; a browser session
// presents none.
func (s Server) Viewer(
	ctx context.Context,
	_ api.ViewerRequestObject,
) (api.ViewerResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	r := from(ctx)
	u, err := s.userOf(ctx, p, r.token, r.expiry)
	if err != nil {
		return nil, err
	}
	return api.Viewer200JSONResponse{Body: u}, nil
}

// Sessions lists the live sessions of the caller's account, oldest first.
func (s Server) Sessions(
	ctx context.Context,
	req api.SessionsRequestObject,
) (api.SessionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var after kernel.ULID
	if req.Params.Cursor != nil {
		c, err := page.Decode[string](sessionsList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		if after, err = kernel.ParseULID(c.ID); err != nil {
			//nolint:wrapcheck // a problem, answered as it is
			return nil, problem.InvalidCursor.Error("The cursor's position is not a session.")
		}
	}
	limit := int(kernel.PageLimit(req.Params.Limit))
	// One more than the page shows whether another follows.
	ss, err := s.auth.Sessions(ctx, p, after, limit+1)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	acts, err := s.actionsOn(ctx, p, entitySession)
	if err != nil {
		return nil, err
	}
	body := api.SessionPage{Items: make([]api.Session, 0, min(len(ss), limit))}
	for _, ss := range ss[:min(len(ss), limit)] {
		body.Items = append(body.Items, view(ss, p.SessionID, acts))
	}
	if len(ss) > limit {
		last := ss[limit-1].ID.String()
		next, err := page.Encode(sessionsList, kernel.Cursor[string]{Key: last, ID: last})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding a string cannot fail
		}
		body.NextCursor = &next
	}
	return api.Sessions200JSONResponse{Body: body}, nil
}

// SessionDelete revokes a session of the caller's account; any other
// answers 404 (decision 0007).
func (s Server) SessionDelete(
	ctx context.Context,
	req api.SessionDeleteRequestObject,
) (api.SessionDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	notFound := problem.NotFound.Error(fmt.Sprintf("No session %q in this account.", req.SessionId))
	id, err := kernel.ParseULID(req.SessionId)
	if err != nil {
		return nil, notFound //nolint:wrapcheck // a problem, answered as it is
	}
	ss, err := s.auth.Revoke(ctx, p, id)
	if errors.Is(err, port.ErrNotFound) {
		return nil, notFound //nolint:wrapcheck // a problem, answered as it is
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	acts, err := s.actionsOn(ctx, p, entitySession)
	if err != nil {
		return nil, err
	}
	return api.SessionDelete200JSONResponse{Body: view(ss, p.SessionID, acts)}, nil
}

// SessionDeleteAll revokes every session of the caller's account but its
// own, and answers the sessions it revoked.
func (s Server) SessionDeleteAll(
	ctx context.Context,
	_ api.SessionDeleteAllRequestObject,
) (api.SessionDeleteAllResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ss, err := s.auth.RevokeOthers(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	acts, err := s.actionsOn(ctx, p, entitySession)
	if err != nil {
		return nil, err
	}
	out := make([]api.Session, 0, len(ss))
	for _, ss := range ss {
		out = append(out, view(ss, p.SessionID, acts))
	}
	return api.SessionDeleteAll200JSONResponse{Body: out}, nil
}

// Logout revokes the caller's own session, and clears a browser
// session's cookie.
func (s Server) Logout(
	ctx context.Context,
	_ api.LogoutRequestObject,
) (api.LogoutResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ss, err := s.auth.Logout(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	acts, err := s.actionsOn(ctx, p, entitySession)
	if err != nil {
		return nil, err
	}
	out := api.Logout200JSONResponse{Body: view(ss, p.SessionID, acts)}
	if from(ctx).browser {
		c := clearedCookie()
		out.Headers.SetCookie = &c
	}
	return out, nil
}

// principal is the caller, which the middleware put on every route but
// the public ones.
func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return kernel.Principal{}, problem.Unauthenticated.Error(refused)
	}
	return p, nil
}

// userOf is p in the reference's User shape, with token as its jwt. Each
// field another domain decides is that domain's answer from the rule its
// operations enforce (decision 0007's grant views), never a copy of the
// rule here: the API keys p created and owns and whether p may create a
// personal key are the apikey service's.
func (s Server) userOf(
	ctx context.Context,
	p kernel.Principal,
	token string,
	until time.Time,
) (api.User, error) {
	u := user(p, token, until)
	h, err := s.keys.Held(ctx, p)
	if err != nil {
		return api.User{}, err //nolint:wrapcheck // the store marks an unreachable database
	}
	u.ApiKeys, u.OwnedApiKeys = keyRefs(h.Created), keyRefs(h.Owned)
	u.CanCreatePersonalApiKey = h.CanCreatePersonal
	if u.Actions, err = s.spaceActions(ctx, p); err != nil {
		return api.User{}, err
	}
	return u, nil
}

// The kinds of entity of the catalogue's account actions whose views list
// the caller's actions.
const (
	entitySession     = "session"
	entityManagedUser = "managed_user"
)

// actionsOn are the actions on an entity of kind entity that p holds, as
// the route table decides each: the catalogue's actions on the kind,
// which are account actions.
func (s Server) actionsOn(
	ctx context.Context,
	p kernel.Principal,
	entity string,
) ([]api.Action, error) {
	held, err := s.authz.Actions(ctx, p, p.AccountID.ULID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	out := []api.Action{}
	for _, a := range held {
		if info, _ := accessapi.ActionOf(a); info.Subject == entity {
			out = append(out, api.Action(a))
		}
	}
	return out, nil
}

// spaceActions are the actions p holds on each space of its account that
// it holds any on, in the tree's order.
func (s Server) spaceActions(ctx context.Context, p kernel.Principal) ([]api.SpaceActions, error) {
	spaces, err := s.spaces.Spaces(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	out := []api.SpaceActions{}
	for _, sp := range spaces {
		held, err := s.authz.Actions(ctx, p, sp.ID)
		if err != nil {
			return nil, err //nolint:wrapcheck // as above
		}
		if len(held) > 0 {
			out = append(out, api.SpaceActions{SpaceID: sp.Slug, Actions: actions(held)})
		}
	}
	return out, nil
}

// actions is as in the API's Action type.
func actions(as []accessapi.Action) []api.Action {
	out := make([]api.Action, 0, len(as))
	for _, a := range as {
		out = append(out, api.Action(a))
	}
	return out
}

func keyRefs(ids []kernel.ULID) []api.ApiKeyRef {
	out := make([]api.ApiKeyRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, api.ApiKeyRef{Id: id.String()})
	}
	return out
}

// user is the part of the User shape the principal alone decides. The
// lists userOf fills, and the security keys that arrive with a later
// phase, are empty, never absent, as the shape requires.
func user(p kernel.Principal, token string, until time.Time) api.User {
	id := p.Login
	if p.Kind == kernel.PrincipalAPIKey {
		id = authn.APIKeySubject + p.ActorID.String()
	}
	return api.User{
		Actions:      []api.SpaceActions{},
		Admin:        p.Admin,
		ApiKeys:      []api.ApiKeyRef{},
		Id:           id,
		Jwt:          token,
		Name:         p.Name,
		OwnedApiKeys: []api.ApiKeyRef{},
		SecurityKeys: []api.SecurityKey{},
		Teams:        nonNil(p.Teams),
		ValidUntil:   unix(until),
	}
}

// view is ss in the reference's Session shape, with acts as its
// allowedActions; current marks the caller's own. type is the actor's
// kind in capitals, such as API_KEY.
func view(ss session.Session, current kernel.ULID, acts []api.Action) api.Session {
	return api.Session{
		Admin:          ss.Admin,
		AllowedActions: acts,
		Channel:        api.SessionChannel(ss.Channel),
		CreatedAt:      unix(ss.CreatedAt),
		Current:        ss.ID == current,
		Email:          ss.Email,
		ExpiresAt:      unix(ss.ExpiresAt),
		Id:             ss.ID.String(),
		IpAddress:      ss.IPAddress,
		IsMachineUser:  ss.ActorKind == kernel.PrincipalAPIKey,
		Login:          ss.Login,
		Name:           ss.Name,
		Teams:          nonNil(ss.Teams),
		Type:           strings.ToUpper(string(ss.ActorKind)),
	}
}

// unix is t in the reference's int32 Unix seconds, saturating rather than
// wrapping past 2038.
func unix(t time.Time) int32 {
	return int32(min(max(t.Unix(), 0), math.MaxInt32)) //nolint:gosec // clamped above
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
