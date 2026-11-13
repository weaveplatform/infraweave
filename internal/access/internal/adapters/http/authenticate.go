package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// Authenticator is accessapi.Authenticator over the authn service: the
// API's, which accepts a bearer token or a browser session's cookie, the
// operator API's, which accepts operator tokens alone, the worker API's,
// which accepts worker session tokens on its worker routes and run tokens
// on its runner routes, or the state backend's, which accepts run tokens
// as HTTP Basic passwords, and nothing else.
type Authenticator struct {
	token func(ctx context.Context, raw string, reach authn.Reach) (kernel.Principal, token.Claims, error)
	// runner authenticates a runner route's run token, on the worker API's
	// authenticator alone.
	runner  func(ctx context.Context, raw string) (kernel.Principal, token.Claims, error)
	browser func(
		ctx context.Context,
		secret string,
		reach authn.Reach,
	) (kernel.Principal, session.Session, error)
	// origin is the public URL's origin, which a cookie-authenticated
	// write must name in its Origin header.
	origin string
	// worker is set on the worker API's authenticator, which alone admits
	// a route of accessapi.Worker or accessapi.Runner, so a request routed
	// to the wrong authenticator is refused rather than authenticated as
	// another surface's caller.
	worker bool
	// state is set on the state backend's authenticator, which alone
	// admits a route of accessapi.State, for the same reason.
	state bool
}

var _ accessapi.Authenticator = Authenticator{}

// NewAuthenticator returns the API's middleware source over auth. A
// browser session's write must carry an Origin of publicURL.
func NewAuthenticator(auth *authn.Service, publicURL string) Authenticator {
	return Authenticator{
		token: auth.Authenticate, browser: auth.AuthenticateBrowser,
		origin: Origin(publicURL),
	}
}

// NewOperatorAuthenticator returns the operator API's middleware source
// over auth.
func NewOperatorAuthenticator(auth *authn.Service) Authenticator {
	return Authenticator{
		token: func(
			ctx context.Context,
			raw string,
			_ authn.Reach,
		) (kernel.Principal, token.Claims, error) {
			return auth.AuthenticateOperator(ctx, raw)
		},
	}
}

// NewWorkerAuthenticator returns the worker API's middleware source over
// auth. It puts the worker session token's or the run token's
// accessapi.Bearer on the request's context beside the principal.
func NewWorkerAuthenticator(auth *authn.Service) Authenticator {
	return Authenticator{
		token: func(
			ctx context.Context,
			raw string,
			_ authn.Reach,
		) (kernel.Principal, token.Claims, error) {
			return auth.AuthenticateWorker(ctx, raw)
		},
		runner: auth.AuthenticateRunner,
		worker: true,
	}
}

// StateUser is the user name of the state backend's HTTP Basic
// credentials, whose password is a run token.
//
// ref: decision 0010 § "Who may call what"
const StateUser = "run"

// StateRealm is the realm of the state backend's challenge.
const StateRealm = "infraweave-state"

// NewStateAuthenticator returns the state backend's middleware source
// over auth: it admits a run token as the password of HTTP Basic
// credentials whose user name is StateUser, which Terraform's and
// OpenTofu's http backend send, and challenges anything else with Basic.
func NewStateAuthenticator(auth *authn.Service) Authenticator {
	return Authenticator{runner: auth.AuthenticateRunner, state: true}
}

// Origin is u's origin, <scheme>://<host>, as a browser sends it in an
// Origin header; empty when u is not an absolute URL.
func Origin(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Scheme == "" || p.Host == "" {
		return ""
	}
	return p.Scheme + "://" + p.Host
}

// request is what the middleware keeps of a request for the handlers: the
// client's address, and once it authenticates, the bearer token with its
// expiry, or for a browser session none and the session's end.
type request struct {
	addr    string
	token   string
	expiry  time.Time
	browser bool
}

type requestKey struct{}

// from is the request the middleware recorded on ctx.
func from(ctx context.Context) request {
	r, _ := ctx.Value(requestKey{}).(request)
	return r
}

// Middleware implements accessapi.Authenticator. A bearer token wins over
// a cookie. A cookie authenticates a GET or HEAD, and a write only when
// its Origin is the public URL's, so another site cannot make a signed-in
// browser write. A store that cannot be read answers 503, never 401, so a
// client retries rather than discards a good credential.
//
// ref: decision 0006 § "Sessions"
func (a Authenticator) Middleware(
	access func(*http.Request) accessapi.Access,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := request{addr: ratelimit.ClientAddr(r)}
			acc := access(r)
			if acc == accessapi.Public {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, rec)))
				return
			}
			if (acc == accessapi.Worker || acc == accessapi.Runner) != a.worker ||
				(acc == accessapi.State) != a.state {
				a.unauthenticated(w)
				return
			}
			reach := reaches[acc]
			var (
				p   kernel.Principal
				err error
			)
			if a.state {
				var c token.Claims
				if user, raw, ok := r.BasicAuth(); ok && user == StateUser && raw != "" {
					p, c, err = a.runner(r.Context(), raw)
					rec.token, rec.expiry = raw, c.Expiry
				} else {
					err = authn.ErrUnauthenticated
				}
			} else if raw, ok := bearer(r); ok {
				var c token.Claims
				if acc == accessapi.Runner {
					p, c, err = a.runner(r.Context(), raw)
				} else {
					p, c, err = a.token(r.Context(), raw, reach)
				}
				rec.token, rec.expiry = raw, c.Expiry
			} else if secret, ok := a.cookie(r); ok {
				var ss session.Session
				p, ss, err = a.browser(r.Context(), secret, reach)
				rec.browser, rec.expiry = true, ss.ExpiresAt
			} else {
				err = authn.ErrUnauthenticated
			}
			if errors.Is(err, authn.ErrUnauthenticated) {
				a.unauthenticated(w)
				return
			}
			if err != nil {
				problem.Failure(w, err)
				return
			}
			ctx := kernel.WithPrincipal(context.WithValue(r.Context(), requestKey{}, rec), p)
			if a.worker {
				ctx = accessapi.WithBearer(
					ctx,
					accessapi.Bearer{ID: p.ActorID, Expires: rec.expiry},
				)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// reaches is the sessions each access admits.
var reaches = map[accessapi.Access]authn.Reach{
	accessapi.Account:           authn.ReachAccount,
	accessapi.Organization:      authn.ReachOrganization,
	accessapi.OrganizationAdmin: authn.ReachOrganizationAdmin,
}

// cookie is the browser session's cookie value when the request may use
// it: always on a safe method, and on a write only with the public URL's
// Origin.
func (a Authenticator) cookie(r *http.Request) (string, bool) {
	if a.browser == nil {
		return "", false
	}
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return c.Value, true
	}
	return c.Value, a.origin != "" && r.Header.Get("Origin") == a.origin
}

// bearer is the token of an Authorization: Bearer header, whose scheme is
// case-insensitive (RFC 9110).
func bearer(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}

// unauthenticated answers 401 with the challenge RFC 6750 asks for, or
// on the state backend the Basic challenge of RFC 7617, which Terraform's
// and OpenTofu's http backend answer.
func (a Authenticator) unauthenticated(w http.ResponseWriter) {
	challenge := "Bearer"
	if a.state {
		challenge = `Basic realm="` + StateRealm + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	problem.Write(w, problem.Unauthenticated.New(refused))
}
