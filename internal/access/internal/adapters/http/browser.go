package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/loopback"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// sessionCookie is the Set-Cookie value that names a browser session for
// the rest of its life (decision 0006 § "Sessions"). __Host- obliges
// Secure, Path=/ and no Domain, so no subdomain can set or read it; Lax
// sends it on a top-level navigation from another site but on no
// cross-site write.
func sessionCookie(b authn.Browser) string {
	life := b.Session.ExpiresAt.Sub(b.Session.CreatedAt)
	return (&http.Cookie{
		Name:     session.CookieName,
		Value:    b.Secret,
		Path:     "/",
		MaxAge:   max(int(life.Seconds()), 1),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}).String()
}

// clearedCookie is the Set-Cookie value that removes the session's cookie;
// its attributes match sessionCookie's, as a browser requires of a
// __Host- cookie it replaces.
func clearedCookie() string {
	return (&http.Cookie{
		Name:     session.CookieName,
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}).String()
}

// CliConfirmationToken issues a CLI token from the caller's browser
// session, sealed to the CLI's public key. A credential that is not a
// member's browser session answers 403; a key that cannot be sealed to
// 422 validation-failed naming pubKey.
func (s Server) CliConfirmationToken(
	ctx context.Context,
	req api.CliConfirmationTokenRequestObject,
) (api.CliConfirmationTokenResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	sealed, err := s.auth.CLIToken(ctx, p, req.Body.PubKey)
	switch {
	case errors.Is(err, authn.ErrNotBrowser):
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.Forbidden.Error("Only a member's browser session issues a CLI token.")
	case errors.Is(err, loopback.ErrKey):
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.ValidationFailed.Error("pubKey: " + err.Error())
	case errors.Is(err, authn.ErrUnauthenticated):
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.Unauthenticated.Error(refused)
	case err != nil:
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	return api.CliConfirmationToken201JSONResponse{Body: sealed}, nil
}

// AuthAccounts lists the accounts that admit the caller's member.
func (s Server) AuthAccounts(
	ctx context.Context,
	_ api.AuthAccountsRequestObject,
) (api.AuthAccountsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	as, err := s.auth.Accounts(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	out := make([]api.AuthAccount, 0, len(as))
	for _, a := range as {
		out = append(out, api.AuthAccount{Id: a.ID.String(), Slug: string(a.Slug), Name: a.Name})
	}
	return api.AuthAccounts200JSONResponse{Body: out}, nil
}

// AuthSessionAccount switches the caller's session to another account
// that admits its member. A browser session's answer sets its new cookie
// and carries no jwt; a token session's carries the new token. An
// account that does not admit the member, or one that does not exist,
// answers 404, as the switcher would not list it.
func (s Server) AuthSessionAccount(
	ctx context.Context,
	req api.AuthSessionAccountRequestObject,
) (api.AuthSessionAccountResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	notFound := problem.NotFound.Error(
		fmt.Sprintf("No account %q admits this member.", req.Body.Account))
	acct, err := kernel.ParseULID(req.Body.Account)
	if err != nil {
		return nil, notFound //nolint:wrapcheck // a problem, answered as it is
	}
	sw, err := s.auth.SwitchAccount(ctx, p, kernel.AccountID{ULID: acct})
	switch {
	case errors.Is(err, authn.ErrNotAdmitted):
		return nil, notFound //nolint:wrapcheck // a problem, answered as it is
	case errors.Is(err, authn.ErrUnauthenticated):
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.Unauthenticated.Error(refused)
	case err != nil:
		return nil, err //nolint:wrapcheck // as above
	}
	out := api.AuthSessionAccount200JSONResponse{
		Body: user(sw.Session.Principal(), sw.Token, sw.Session.ExpiresAt),
	}
	if sw.Secret != "" {
		c := sessionCookie(authn.Browser{Secret: sw.Secret, Session: sw.Session})
		out.Headers.SetCookie = &c
	}
	return out, nil
}
