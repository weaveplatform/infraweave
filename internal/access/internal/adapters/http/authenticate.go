package http

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// Authenticator is accessapi.Authenticator over the authn service.
type Authenticator struct{ auth *authn.Service }

var _ accessapi.Authenticator = Authenticator{}

// NewAuthenticator returns the middleware's source over auth.
func NewAuthenticator(auth *authn.Service) Authenticator { return Authenticator{auth: auth} }

// request is what the middleware keeps of a request for the handlers: the
// client's address, and the bearer token with its expiry once it
// authenticates.
type request struct {
	addr   string
	token  string
	expiry time.Time
}

type requestKey struct{}

// from is the request the middleware recorded on ctx.
func from(ctx context.Context) request {
	r, _ := ctx.Value(requestKey{}).(request)
	return r
}

// Middleware implements accessapi.Authenticator. A store that cannot be
// read answers 503, never 401, so a client retries rather than discards a
// good token.
func (a Authenticator) Middleware(public func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := request{addr: ratelimit.ClientAddr(r)}
			if public(r) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, rec)))
				return
			}
			raw, ok := bearer(r)
			if !ok {
				unauthenticated(w)
				return
			}
			p, c, err := a.auth.Authenticate(r.Context(), raw)
			if errors.Is(err, authn.ErrUnauthenticated) {
				unauthenticated(w)
				return
			}
			if err != nil {
				problem.Failure(w, err)
				return
			}
			rec.token, rec.expiry = raw, c.Expiry
			ctx := kernel.WithPrincipal(context.WithValue(r.Context(), requestKey{}, rec), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearer is the token of an Authorization: Bearer header, whose scheme is
// case-insensitive (RFC 9110).
func bearer(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}

// unauthenticated answers 401 with the challenge RFC 6750 asks for.
func unauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	problem.Write(w, problem.Unauthenticated.New(refused))
}
