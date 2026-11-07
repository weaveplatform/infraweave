package app

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/app/config"
	featuresassembly "github.com/weaveplatform/infraweave/internal/features/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	operatorapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
)

// BaseURL is the API's prefix, the server URL of api/api.openapi.yaml.
const BaseURL = "/api/v1"

// OperatorBaseURL is the operator API's prefix, the server URL of
// api/operator.openapi.yaml.
const OperatorBaseURL = "/operator/v1"

// apiServer is the API's strict server. Each domain's partial embeds here
// at depth one under its domain's name, and UnimplementedStrict at depth
// two through unimplemented, so a domain's operation shadows its 501 and
// an operation two domains implement is ambiguous, which the assertion
// below fails to compile. The installation's own operations, which no
// domain owns, read queues. Domains record and write objects through
// blobs.
type apiServer struct {
	accessassembly.AccessAPI
	unimplemented
	queues QueueStats
	blobs  *blobs.Objects
}

// unimplemented holds UnimplementedStrict one level below the partials.
type unimplemented struct{ api.UnimplementedStrict }

// operatorServer is the operator API's strict server, composed as
// apiServer is: each domain's partial at depth one, the 501 stubs at two.
// The installation's own operations, which no domain owns, reach
// platform.
type operatorServer struct {
	accessassembly.AccessOperatorAPI
	featuresassembly.FeaturesOperatorAPI
	unimplementedOperator
	platform Platform
}

type unimplementedOperator struct {
	operatorapi.UnimplementedStrict
}

var _ operatorapi.StrictServerInterface = operatorServer{}

// Blobs is object storage as the API role holds it: records written
// through an objectstore.Writer, which has no Delete, so serve can never
// delete an object (decision 0005), and the fs driver's /blobs/{token}
// routes, or nil for another driver.
type Blobs struct {
	Objects *blobs.Objects
	Routes  func(chi.Router)
}

var _ api.StrictServerInterface = apiServer{}

// API is the API listener's handler: the API under BaseURL and the
// operator API under OperatorBaseURL behind c's rate limits and
// idempotency keys, whose state g holds, with each domain's operations
// from d, the installation's own from p and objects in b, and the fs
// driver's signed-URL routes beside them. Each API authenticates with its
// own authenticator from d, so neither accepts the other's tokens. It
// serves no probe; those are the admin listener's alone, and are never
// rate-limited.
func API(
	log *slog.Logger,
	c config.Server,
	g Guards,
	d Domains,
	p Platform,
	b Blobs,
) http.Handler {
	var gated Gated
	if d.Features != nil {
		gated = d.Features.Middleware
	}
	r := Guarded(log, c, apiOperations(), g, byPrefix{api: d.Auth, operator: d.OperatorAuth}, gated)
	if b.Routes != nil {
		b.Routes(r)
	}
	d.Access.Routes(r)
	strict := api.NewStrictHandlerWithOptions(
		apiServer{AccessAPI: d.Access.API(), queues: p.Queues, blobs: b.Objects},
		nil,
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  problem.RequestError,
			ResponseErrorHandlerFunc: problem.ResponseError,
		},
	)
	api.HandlerWithOptions(strict, api.ChiServerOptions{
		BaseURL:          BaseURL,
		BaseRouter:       r,
		ErrorHandlerFunc: problem.RequestError,
	})
	operator := operatorapi.NewStrictHandlerWithOptions(
		operatorServer{
			AccessOperatorAPI:   d.Access.OperatorAPI(),
			FeaturesOperatorAPI: d.Features.OperatorAPI(d.Access.OrganizationID),
			platform:            p,
		},
		nil,
		operatorapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  problem.RequestError,
			ResponseErrorHandlerFunc: problem.ResponseError,
		},
	)
	operatorapi.HandlerWithOptions(operator, operatorapi.ChiServerOptions{
		BaseURL:          OperatorBaseURL,
		BaseRouter:       r,
		ErrorHandlerFunc: problem.RequestError,
	})
	return r
}

// byPrefix authenticates a request under OperatorBaseURL with operator
// and every other with api; a nil one refuses every credential.
type byPrefix struct{ api, operator accessapi.Authenticator }

func (b byPrefix) Middleware(
	access func(*http.Request) accessapi.Access,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		api := orRefuse(b.api).Middleware(access)(next)
		operator := orRefuse(b.operator).Middleware(access)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p := routePath(
				r,
			); p == OperatorBaseURL ||
				strings.HasPrefix(p, OperatorBaseURL+"/") {
				operator.ServeHTTP(w, r)
				return
			}
			api.ServeHTTP(w, r)
		})
	}
}

func orRefuse(a accessapi.Authenticator) accessapi.Authenticator {
	if a == nil {
		return refuseAll{}
	}
	return a
}
