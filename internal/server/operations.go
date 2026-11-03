package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/server/gen/deploy"
	"github.com/weaveplatform/infraweave/internal/server/idempotency"
	"github.com/weaveplatform/infraweave/internal/server/ratelimit"
)

// Operation is what the controls in front of the router need to know about
// the operation a request is routed to.
type Operation struct {
	// Cost is the operation's x-infraweave-cost.
	Cost ratelimit.Class
	// Search marks a search, which changes nothing and so ignores
	// Idempotency-Key.
	Search bool
}

// Operations maps "METHOD pattern", a method and the chi route pattern it is
// mounted at, to its operation.
type Operations map[string]Operation

var errSpec = errors.New("invalid operation in the deploy API")

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
			}
		}
	}
	return ops, nil
}

// deployOperations reads the embedded spec once. It cannot fail at run time
// unless the generated package is corrupt, which TestUnitDeployOperations
// would already have caught.
var deployOperations = sync.OnceValue(func() Operations {
	doc, err := deploy.GetSpec()
	if err != nil {
		panic(fmt.Sprintf("load the embedded deploy API: %v", err))
	}
	ops, err := operationsOf(doc, BaseURL)
	if err != nil {
		panic(err.Error())
	}
	return ops
})

// Guarded is an API listener router: request ID and recovery, then decision
// 0003's controls in front of every route registered on it. The operator
// switch runs first, then the rate limit, then idempotency keys, so a request
// one rejects never reaches the next and a rejected request never reaches
// the key store. ops names the operations its routes serve; a request no
// operation serves, such as a 404, still draws from the standard budget.
func Guarded(log *slog.Logger, c Config, ops Operations) *chi.Mux {
	var r *chi.Mux
	resolve := func(req *http.Request) (Operation, bool) {
		pattern := r.Find(chi.NewRouteContext(), req.Method, routePath(req))
		op, ok := ops[req.Method+" "+pattern]
		return op, ok
	}
	caller := ratelimit.ClientAddr
	r = newRouter(log,
		ratelimit.Disabled(ratelimit.NewCallers(c.RateLimit.DisabledCallers), caller),
		ratelimit.Middleware(c.RateLimit, ratelimit.NewMemory(), caller,
			func(req *http.Request) ratelimit.Class {
				op, _ := resolve(req)
				return op.Cost
			}, time.Now),
		idempotency.Middleware(idempotency.NewMemory(c.Idempotency.TTL, time.Now), caller,
			func(req *http.Request) bool {
				op, ok := resolve(req)
				return ok && !op.Search
			}, log),
	)
	return r
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
