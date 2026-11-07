package http

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
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
	accounts Accounts
	settings *settings.Service
	orgs     *orgadmin.Service
}

// Accounts reads an account; port.Tenancy is one.
type Accounts interface {
	Account(ctx context.Context, id kernel.AccountID) (account.Account, error)
}

// NewServer returns the partial serving through auth, keys, accounts, st
// and, for the Organization tag, orgs.
func NewServer(
	auth *authn.Service,
	keys *apikey.Service,
	accounts Accounts,
	st *settings.Service,
	orgs *orgadmin.Service,
) Server {
	return Server{auth: auth, keys: keys, accounts: accounts, settings: st, orgs: orgs}
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
	return api.ApiKeyUser200JSONResponse{
		Body: user(g.Session.Principal(), g.Token, g.Claims.Expiry),
	}, nil
}

// Viewer is the caller, with the token it presented; a browser session
// presents none.
func (Server) Viewer(
	ctx context.Context,
	_ api.ViewerRequestObject,
) (api.ViewerResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	r := from(ctx)
	return api.Viewer200JSONResponse{Body: user(p, r.token, r.expiry)}, nil
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
	body := api.SessionPage{Items: make([]api.Session, 0, min(len(ss), limit))}
	for _, ss := range ss[:min(len(ss), limit)] {
		body.Items = append(body.Items, view(ss, p.SessionID))
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
	return api.SessionDelete200JSONResponse{Body: view(ss, p.SessionID)}, nil
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
	out := make([]api.Session, 0, len(ss))
	for _, ss := range ss {
		out = append(out, view(ss, p.SessionID))
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
	out := api.Logout200JSONResponse{Body: view(ss, p.SessionID)}
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

// user is p in the reference's User shape, with token as its jwt. The
// lists of what arrives with later phases (space actions, API keys and
// security keys) are empty, never absent, as the shape requires.
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

// view is ss in the reference's Session shape; current marks the caller's
// own. type is the actor's kind in capitals, such as API_KEY.
func view(ss session.Session, current kernel.ULID) api.Session {
	return api.Session{
		Admin:         ss.Admin,
		Channel:       api.SessionChannel(ss.Channel),
		CreatedAt:     unix(ss.CreatedAt),
		Current:       ss.ID == current,
		Email:         ss.Email,
		ExpiresAt:     unix(ss.ExpiresAt),
		Id:            ss.ID.String(),
		IpAddress:     ss.IPAddress,
		IsMachineUser: ss.ActorKind == kernel.PrincipalAPIKey,
		Login:         ss.Login,
		Name:          ss.Name,
		Teams:         nonNil(ss.Teams),
		Type:          strings.ToUpper(string(ss.ActorKind)),
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
