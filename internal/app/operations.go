package app

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	operatorapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
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
	// Organization marks an operation whose x-infraweave-access is
	// organization: it serves any session of an organization, with an
	// account or none, such as the viewer or the account switcher. Every
	// other operation needs a credential of an account (decision 0006).
	Organization bool
	// Body is the request body of an operation whose JSON body schema sets
	// additionalProperties: false, which strictBodies checks before the
	// handler; nil for every other operation, whose body the generated
	// handler decodes leniently.
	Body *openapi3.RequestBody
	// Gate is the operation's x-infraweave-feature, a capability of the
	// catalogue, and the options its request body's properties name.
	Gate featuresapi.Gate
}

// Operations maps "METHOD pattern", a method and the chi route pattern it is
// mounted at, to its operation.
type Operations map[string]Operation

var errSpec = errors.New("invalid operation in the API")

// operationsOf reads each operation of doc, mounted under base. A missing
// x-infraweave-cost is standard, as decision 0003 says; one that names no
// class fails, so a typo in the spec cannot silently cost standard. Every
// operation names its capability in x-infraweave-feature (decision 0021),
// and one that names none, or a name the catalogue holds as no capability,
// fails, so no operation goes ungated.
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
			org := false
			if v, ok := op.Extensions["x-infraweave-access"]; ok {
				if v != "organization" {
					return nil, fmt.Errorf("%w: %s %s has x-infraweave-access %v",
						errSpec, method, path, v)
				}
				org = true
			}
			gate, err := gateOf(op)
			if err != nil {
				return nil, fmt.Errorf("%w: %s %s %w", errSpec, method, path, err)
			}
			ops[method+" "+base+path] = Operation{
				Cost:         cost,
				Search:       style == "search" || strings.HasSuffix(path, "/search"),
				Public:       op.Security != nil && len(*op.Security) == 0,
				Organization: org,
				Body:         strictBody(op),
				Gate:         gate,
			}
		}
	}
	return ops, nil
}

var errFeature = errors.New("names no feature of its kind")

// gateOf reads op's capability, and the options its JSON body's top-level
// properties carry as x-infraweave-feature: {feature, value}, where no
// value means any value but null.
func gateOf(op *openapi3.Operation) (featuresapi.Gate, error) {
	name, _ := op.Extensions["x-infraweave-feature"].(string)
	if f, ok := features.Lookup(name); !ok || f.Kind != features.Capability {
		return featuresapi.Gate{}, fmt.Errorf("x-infraweave-feature %q %w", name, errFeature)
	}
	g := featuresapi.Gate{Feature: name}
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		return g, nil
	}
	mt := op.RequestBody.Value.Content.Get(jsonType)
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		return g, nil
	}
	props := mt.Schema.Value.Properties
	for _, prop := range slices.Sorted(maps.Keys(props)) {
		ref := props[prop]
		ext, ok := ref.Extensions["x-infraweave-feature"].(map[string]any)
		if !ok && ref.Value != nil {
			ext, ok = ref.Value.Extensions["x-infraweave-feature"].(map[string]any)
		}
		if !ok {
			continue
		}
		option, _ := ext["feature"].(string)
		if f, ok := features.Lookup(option); !ok || f.Kind != features.Option {
			return featuresapi.Gate{}, fmt.Errorf("property %s's x-infraweave-feature %q %w",
				prop, option, errFeature)
		}
		value, _ := ext["value"].(string)
		g.Options = append(g.Options, featuresapi.OptionGate{
			Property: prop, Value: value, Feature: option,
		})
	}
	return g, nil
}

// strictBody is op's request body when its JSON schema forbids properties
// it does not name.
func strictBody(op *openapi3.Operation) *openapi3.RequestBody {
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	mt := op.RequestBody.Value.Content.Get(jsonType)
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		return nil
	}
	if ap := mt.Schema.Value.AdditionalProperties.Has; ap == nil || *ap {
		return nil
	}
	return op.RequestBody.Value
}

// apiOperations reads the embedded specs once: the API's under BaseURL
// and the operator API's under OperatorBaseURL, which one router serves.
// It cannot fail at run time unless a generated package is corrupt, which
// TestUnitAPIOperations would already have caught.
var apiOperations = sync.OnceValue(func() Operations {
	ops := Operations{}
	for base, spec := range map[string]func() (*openapi3.T, error){
		BaseURL:         api.GetSpec,
		OperatorBaseURL: operatorapi.GetSpec,
	} {
		doc, err := spec()
		if err != nil {
			panic(fmt.Sprintf("load the embedded spec of %s: %v", base, err))
		}
		some, err := operationsOf(doc, base)
		if err != nil {
			panic(err.Error())
		}
		maps.Copy(ops, some)
	}
	// The break-glass login is a page, not an operation of either API, but
	// the same controls stand in front of it: no credential, and each
	// sign-in an expensive draw on the client's address, which slows a
	// password guesser to the expensive class's rate. It names no feature,
	// so no switch-off or tier can lock the break-glass admin out.
	ops[http.MethodGet+" "+accessapi.LoginPattern] = Operation{Public: true}
	ops[http.MethodPost+" "+accessapi.LoginPattern] = Operation{
		Public: true,
		Cost:   ratelimit.Expensive,
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

// Gated builds the feature middleware from a resolver of each request's
// gate; the features domain's Middleware is one.
type Gated func(gate func(*http.Request) (featuresapi.Gate, bool)) func(http.Handler) http.Handler

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
// authenticates and draws from the standard budget. gated, when not nil,
// builds decision 0021's feature middleware, which runs right after
// authentication, so a refused feature costs no budget and stores no
// idempotent answer; it gates every operation that names a capability.
func Guarded(
	log *slog.Logger,
	c config.Server,
	ops Operations,
	g Guards,
	auth accessapi.Authenticator,
	gated Gated,
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
	if gated == nil {
		gated = func(func(*http.Request) (featuresapi.Gate, bool)) func(http.Handler) http.Handler {
			return func(next http.Handler) http.Handler { return next }
		}
	}
	r = middleware.NewRouter(log,
		middleware.AccessLog(log),
		auth.Middleware(func(req *http.Request) accessapi.Access {
			op, _ := resolve(req)
			return op.access()
		}),
		gated(func(req *http.Request) (featuresapi.Gate, bool) {
			op, ok := resolve(req)
			return op.Gate, ok && op.Gate.Feature != ""
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
		strictBodies(func(req *http.Request) *openapi3.RequestBody {
			op, _ := resolve(req)
			return op.Body
		}),
	)
	return r
}

// access is the credential the operation needs.
func (o Operation) access() accessapi.Access {
	switch {
	case o.Public:
		return accessapi.Public
	case o.Organization:
		return accessapi.Organization
	}
	return accessapi.Account
}

// jsonType is the media type every request body of the API is.
const jsonType = "application/json"

// strictBodies checks the body of a request whose operation has a strict
// one against its schema (decision 0022), answering 400 invalid-request
// for an unknown field, a missing required one or a wrong type before the
// handler decodes it. A body with no Content-Type is checked as JSON, as
// the generated handler decodes it.
func strictBodies(body func(*http.Request) *openapi3.RequestBody) func(http.Handler) http.Handler {
	opts := &openapi3filter.Options{SkipSettingDefaults: true}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rb := body(r)
			if rb == nil {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("Content-Type") == "" {
				r.Header.Set("Content-Type", jsonType)
			}
			in := &openapi3filter.RequestValidationInput{Request: r, Options: opts}
			if err := openapi3filter.ValidateRequestBody(r.Context(), in, rb); err != nil {
				problem.RequestError(w, r, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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

func (refuseAll) Middleware(
	access func(*http.Request) accessapi.Access,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if access(r) == accessapi.Public {
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
