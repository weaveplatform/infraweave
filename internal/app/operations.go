package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// Operation is what the controls in front of the router need to know about
// the operation a request is routed to.
type Operation struct {
	// Cost is the operation's x-infraweave-cost.
	Cost ratelimit.Class
	// Search marks a search, which changes nothing and so ignores
	// Idempotency-Key.
	Search bool
	// Public marks an operation that takes no credential, whose security
	// is the empty list, such as POST /auth/token. Every other operation
	// requires the API's global bearerAuth.
	Public bool
}

// Operations maps "METHOD pattern", a method and the chi route pattern it is
// mounted at, to its operation.
type Operations map[string]Operation

var errSpec = errors.New("invalid operation in the API")

// operationsOf reads each operation of doc, mounted under base. A missing
// x-infraweave-cost is standard, as decision 0003 says; one that names no
// class fails, so a typo in the spec cannot silently cost standard.
func operationsOf(doc *openapi3.T, base string) (Operations, error) {
	ops := Operations{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			cost := ratelimit.Standard
			if v, ok := op.Extensions["x-infraweave-cost"]; ok {
				s, _ := v.(string)
				cost = ratelimit.Class(s)
				if cost != ratelimit.Standard && cost != ratelimit.Expensive {
					return nil, fmt.Errorf("%w: %s %s has x-infraweave-cost %v",
						errSpec, method, path, v)
				}
			}
			style, _ := op.Extensions["x-rest-style"].(string)
			ops[method+" "+base+path] = Operation{
				Cost:   cost,
				Search: style == "search" || strings.HasSuffix(path, "/search"),
				Public: op.Security != nil && len(*op.Security) == 0,
			}
		}
	}
	return ops, nil
}

// apiOperations reads the embedded spec once. It cannot fail at run time
// unless the generated package is corrupt, which TestUnitAPIOperations
// would already have caught.
var apiOperations = sync.OnceValue(func() Operations {
	doc, err := api.GetSpec()
	if err != nil {
		panic(fmt.Sprintf("load the embedded API: %v", err))
	}
	ops, err := operationsOf(doc, BaseURL)
	if err != nil {
		panic(err.Error())
	}
	return ops
})

// Guards hold the state of decision 0003's controls: the operator's switch,
// the rate-limit buckets and the idempotency keys. serve keeps all three in
// PostgreSQL, shared by every replica (postgresGuards).
type Guards struct {
	Switch  ratelimit.Switch
	Buckets ratelimit.Store
	Keys    idempotency.Store
}

// Guarded is an API listener router: request ID and recovery, the access
// log, authentication, then decision 0003's controls in front of every
// route registered on it, so a request a control rejects is logged too.
// auth authenticates every request but those to a Public operation; nil
// refuses every credential. The operator switch runs next, then the rate
// limit, then idempotency keys, so a request one rejects never reaches the
// next and a rejected request never reaches the key store. Each control
// keys on the caller, the principal's <kind>:<actor ID> (decision 0006),
// or the client's address on a public route. ops names the operations its
// routes serve; a request no operation serves, such as a 404, still
// authenticates and draws from the standard budget.
func Guarded(
	log *slog.Logger,
	c config.Server,
	ops Operations,
	g Guards,
	auth accessapi.Authenticator,
) *chi.Mux {
	var r *chi.Mux
	resolve := func(req *http.Request) (Operation, bool) {
		pattern := r.Find(chi.NewRouteContext(), req.Method, routePath(req))
		op, ok := ops[req.Method+" "+pattern]
		return op, ok
	}
	if auth == nil {
		auth = refuseAll{}
	}
	r = middleware.NewRouter(log,
		middleware.AccessLog(log),
		auth.Middleware(func(req *http.Request) bool {
			op, _ := resolve(req)
			return op.Public
		}),
		ratelimit.Disabled(g.Switch, caller),
		ratelimit.Middleware(c.RateLimit, g.Buckets, caller,
			func(req *http.Request) ratelimit.Class {
				op, _ := resolve(req)
				return op.Cost
			}),
		idempotency.Middleware(g.Keys, caller,
			func(req *http.Request) bool {
				op, ok := resolve(req)
				return ok && !op.Search
			}, log),
	)
	return r
}

// caller is the key of decision 0003's controls: the principal's, or the
// client's address on a route no credential authenticates.
func caller(r *http.Request) string {
	if p, ok := kernel.PrincipalFrom(r.Context()); ok {
		return p.Caller()
	}
	return ratelimit.ClientAddr(r)
}

// refuseAll is the authenticator of a router built with none: every route
// that needs a credential answers 401.
type refuseAll struct{}

func (refuseAll) Middleware(public func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if public(r) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem.Write(
				w,
				problem.Unauthenticated.New("The request carries no valid credential."),
			)
		})
	}
}

// routePath is the path chi routes by: the escaped path when the request has
// one, so the operation found is the one the router will serve.
func routePath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	if r.URL.Path == "" {
		return "/"
	}
	return r.URL.Path
}
