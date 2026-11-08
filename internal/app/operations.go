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
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
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
	// other operation needs a credential of an account.
	//
	// ref: decision 0006
	Organization bool
	// OrganizationAdmin marks an operation whose x-infraweave-access is
	// organization-admin, the Organization tag's: it serves the principals
	// that may hold the organization role and organization tokens, which no
	// other operation accepts.
	//
	// ref: decision 0006
	OrganizationAdmin bool
	// Body is the request body of an operation whose JSON body schema sets
	// additionalProperties: false, which strictBodies checks before the
	// handler; nil for every other operation, whose body the generated
	// handler decodes leniently.
	Body *openapi3.RequestBody
	// Gate is the operation's x-infraweave-feature, a capability of the
	// catalogue, and the options its request body's properties name.
	Gate featuresapi.Gate
	// Required is the API operation's x-infraweave-action and
	// x-infraweave-subject, which the authorization middleware decides on
	// the caller's roles; nil for the operator API's operations and the
	// routes beside the APIs, which no action decides.
	//
	// ref: decision 0007
	Required *accessapi.Required
}

// Operations maps "METHOD pattern", a method and the chi route pattern it is
// mounted at, to its operation.
type Operations map[string]Operation

var errSpec = errors.New("invalid operation in the API")

// operationsOf reads each operation of doc, mounted under base. A missing
// x-infraweave-cost is standard; one that names no class fails, so a typo
// in the spec cannot silently cost standard. Every operation names its
// capability in x-infraweave-feature, and one that names none, or a name
// the catalogue holds as no capability, fails, so no operation goes
// ungated. With authorized, every operation names its action and subject,
// and one that lacks either or names an action the catalogue lacks fails,
// so no operation goes undecided.
//
// ref: decisions 0003, 0007 and 0021
func operationsOf(doc *openapi3.T, base string, authorized bool) (Operations, error) {
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
			var org, admin bool
			switch v, ok := op.Extensions["x-infraweave-access"]; {
			case !ok:
			case v == "organization":
				org = true
			case v == "organization-admin":
				admin = true
			default:
				return nil, fmt.Errorf("%w: %s %s has x-infraweave-access %v",
					errSpec, method, path, v)
			}
			gate, err := gateOf(op)
			if err != nil {
				return nil, fmt.Errorf("%w: %s %s %w", errSpec, method, path, err)
			}
			var required *accessapi.Required
			if authorized {
				if required, err = requiredOf(op); err != nil {
					return nil, fmt.Errorf("%w: %s %s %w", errSpec, method, path, err)
				}
			}
			ops[method+" "+base+path] = Operation{
				Cost:              cost,
				Search:            style == "search" || strings.HasSuffix(path, "/search"),
				Public:            op.Security != nil && len(*op.Security) == 0,
				Organization:      org,
				OrganizationAdmin: admin,
				Body:              strictBody(op),
				Gate:              gate,
				Required:          required,
			}
		}
	}
	return ops, nil
}

var errAction = errors.New("names no action of the catalogue")

// requiredOf reads op's action and subject.
func requiredOf(op *openapi3.Operation) (*accessapi.Required, error) {
	name, _ := op.Extensions["x-infraweave-action"].(string)
	a := accessapi.Action(name)
	_, known := accessapi.ActionOf(a)
	switch a {
	case accessapi.ActionAccountAdmin, accessapi.ActionOrganizationAdmin,
		accessapi.ActionAuthenticated:
		known = true
	}
	if !known {
		return nil, fmt.Errorf("x-infraweave-action %q %w", name, errAction)
	}
	raw, _ := op.Extensions["x-infraweave-subject"].(string)
	s, err := accessapi.ParseSubject(raw)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the subject
	}
	return &accessapi.Required{Action: a, Subject: s}, nil
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
		// Operator tokens alone reach the operator API, which no account
		// grant decides.
		some, err := operationsOf(doc, base, base == BaseURL)
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
	// The authorization server for MCP clients takes no credential: a
	// client registers, authorizes and redeems before it holds one. Each
	// registration is an expensive draw on the client's address, because
	// anyone may register, and none names a feature, so the protocol
	// answers whatever an organization's tier holds; the consent
	// operations, which grant, are the API's and gated there.
	// ref: decision 0006
	ops[http.MethodGet+" "+accessapi.OAuthMetadataPattern] = Operation{Public: true}
	ops[http.MethodPost+" "+accessapi.OAuthRegisterPattern] = Operation{
		Public: true,
		Cost:   ratelimit.Expensive,
	}
	ops[http.MethodGet+" "+accessapi.OAuthAuthorizePattern] = Operation{Public: true}
	ops[http.MethodPost+" "+accessapi.OAuthTokenPattern] = Operation{Public: true}
	// The fs driver's signed URLs carry their credential in the token,
	// which the driver verifies; a bearer check in front would answer 401
	// to every one. Each draws on the standard budget of the client's
	// address and names no feature, since the URL was signed by an
	// operation already gated.
	// ref: decision 0005
	ops[http.MethodGet+" "+fs.Pattern] = Operation{Public: true}
	ops[http.MethodPut+" "+fs.Pattern] = Operation{Public: true}
	return ops
})

// Guards hold the state of the API's controls: the operator's switch, the
// rate-limit buckets and the idempotency keys. serve keeps all three in
// PostgreSQL, shared by every replica (postgresGuards).
//
// ref: decision 0003
type Guards struct {
	Switch  ratelimit.Switch
	Buckets ratelimit.Store
	Keys    idempotency.Store
}

// Gated builds the feature middleware from a resolver of each request's
// gate; the features domain's Middleware is one.
type Gated func(gate func(*http.Request) (featuresapi.Gate, bool)) func(http.Handler) http.Handler

// Guarded is an API listener router: request ID and recovery, the access
// log, authentication, then the operator switch, rate limits and
// idempotency keys in front of every route registered on it, so a request
// a control rejects is logged too.
// auth authenticates every request but those to a Public operation; nil
// refuses every credential. The operator switch runs next, then the rate
// limit, then idempotency keys, so a request one rejects never reaches the
// next and a rejected request never reaches the key store. Each control
// keys on the caller, the principal's <kind>:<actor ID>, or the client's
// address on a public route. ops names the operations its routes serve; a
// request no operation serves, such as a 404, still authenticates and draws
// from the standard budget. gated, when not nil, builds the feature
// middleware, which runs right after authentication, so a refused feature
// costs no budget and stores no idempotent answer; it gates every operation
// that names a capability. Between the two, a route the router serves that
// ops lacks answers 403, and require, when not nil, decides each API
// operation's action on its subject, so a refused action costs no budget
// either.
//
// ref: decisions 0003, 0006, 0007 and 0021
func Guarded(
	log *slog.Logger,
	c config.Server,
	ops Operations,
	g Guards,
	auth accessapi.Authenticator,
	require accessapi.Requirer,
	gated Gated,
) *chi.Mux {
	var r *chi.Mux
	find := func(req *http.Request) (string, *chi.Context) {
		rctx := chi.NewRouteContext()
		return r.Find(rctx, req.Method, routePath(req)), rctx
	}
	resolve := func(req *http.Request) (Operation, bool) {
		pattern, _ := find(req)
		op, ok := ops[req.Method+" "+pattern]
		return op, ok
	}
	if auth == nil {
		auth = refuseAll{}
	}
	if require == nil {
		require = passAll{}
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
		unlisted(func(req *http.Request) bool {
			pattern, _ := find(req)
			_, ok := ops[req.Method+" "+pattern]
			return pattern != "" && !ok
		}),
		require.Middleware(func(req *http.Request) (accessapi.Required, bool) {
			pattern, rctx := find(req)
			op, ok := ops[req.Method+" "+pattern]
			if !ok || op.Required == nil {
				return accessapi.Required{}, false
			}
			out := *op.Required
			out.Params = map[string]string{}
			for i, k := range rctx.URLParams.Keys {
				out.Params[k] = rctx.URLParams.Values[i]
			}
			return out, true
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
	case o.OrganizationAdmin:
		return accessapi.OrganizationAdmin
	}
	return accessapi.Account
}

// jsonType is the media type every request body of the API is.
const jsonType = "application/json"

// strictBodies checks the body of a request whose operation has a strict
// one against its schema, answering 400 invalid-request for an unknown
// field, a missing required one or a wrong type before the handler decodes
// it. A body with no Content-Type is checked as JSON, as the generated
// handler decodes it.
//
// ref: decision 0022
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

// caller is the key of the rate limits and idempotency keys: the
// principal's, or the client's address on a route no credential
// authenticates.
//
// ref: decision 0003
func caller(r *http.Request) string {
	if p, ok := kernel.PrincipalFrom(r.Context()); ok {
		return p.Caller()
	}
	return ratelimit.ClientAddr(r)
}

// unlisted answers 403 to a request for a route the router serves but the
// operations lack, so a route mounted without a route-table entry is
// never served undecided: authorization denies by default.
//
// ref: decision 0007
func unlisted(listed func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if listed(r) {
				problem.Write(w, problem.Forbidden.New("No route-table entry decides this route."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// passAll is the requirer of a router built with none, for tests of the
// other controls: it decides nothing.
type passAll struct{}

func (passAll) Middleware(
	func(*http.Request) (accessapi.Required, bool),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
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
