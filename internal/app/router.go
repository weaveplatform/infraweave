package app

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/app/config"
	configurationassembly "github.com/weaveplatform/infraweave/internal/configuration/assembly"
	expressionsassembly "github.com/weaveplatform/infraweave/internal/expressions/assembly"
	featuresassembly "github.com/weaveplatform/infraweave/internal/features/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	operatorapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	runsassembly "github.com/weaveplatform/infraweave/internal/runs/assembly"
	stacksassembly "github.com/weaveplatform/infraweave/internal/stacks/assembly"
	vcsassembly "github.com/weaveplatform/infraweave/internal/vcs/assembly"
	vendorsassembly "github.com/weaveplatform/infraweave/internal/vendors/assembly"
	workersassembly "github.com/weaveplatform/infraweave/internal/workers/assembly"
)

// BaseURL is the API's prefix, the server URL of api/api.openapi.yaml.
const BaseURL = "/api/v1"

// OperatorBaseURL is the operator API's prefix, the server URL of
// api/operator.openapi.yaml.
const OperatorBaseURL = "/operator/v1"

// WorkerBaseURL is the worker API's prefix, the server URL of
// api/worker.openapi.yaml.
const WorkerBaseURL = "/worker/v1"

// apiServer is the API's strict server. Each domain's partial embeds here
// at depth one under its domain's name, and UnimplementedStrict at depth
// two through unimplemented, so a domain's operation shadows its 501 and
// an operation two domains implement is ambiguous, which the assertion
// below fails to compile. The installation's own operations, which no
// domain owns, read queues. Domains record and write objects through
// blobs.
type apiServer struct {
	accessassembly.AccessAPI
	featuresassembly.FeaturesAPI
	workersassembly.WorkersAPI
	vendorsassembly.VendorsAPI
	stacksassembly.StacksAPI
	configurationassembly.ConfigurationAPI
	runsassembly.RunsAPI
	vcsassembly.VCSAPI
	expressionsassembly.ExpressionsAPI
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
// delete an object, and the fs driver's /blobs/{token} routes, or nil for
// another driver.
//
// ref: decision 0005
type Blobs struct {
	Objects *blobs.Objects
	Routes  func(chi.Router)
}

var _ api.StrictServerInterface = apiServer{}

// API is the API listener's handler: the API under BaseURL, the
// operator API under OperatorBaseURL and the worker API under
// WorkerBaseURL behind c's rate limits and idempotency keys, whose state
// g holds, with each domain's operations from d, the installation's own
// from p and objects in b, and the fs driver's signed-URL routes beside
// them. Each API authenticates with its own authenticator, so none
// accepts another's tokens. It
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
	var require accessapi.Requirer
	if d.Access != nil {
		require = d.Access.Requirer()
	}
	r := Guarded(log, c, apiOperations(), g,
		byPrefix{api: d.Auth, operator: d.OperatorAuth, worker: d.WorkerAuth}, require, gated)
	if b.Routes != nil {
		b.Routes(r)
	}
	d.Access.Routes(r)
	strict := api.NewStrictHandlerWithOptions(
		apiServer{
			AccessAPI:        d.Access.API(),
			FeaturesAPI:      d.Features.API(d.Access.Seats),
			WorkersAPI:       d.Workers.API(),
			VendorsAPI:       d.Vendors.API(),
			StacksAPI:        d.Stacks.API(),
			ConfigurationAPI: d.Configuration.API(),
			RunsAPI:          d.Runs.API(),
			VCSAPI:           d.VCS.API(),
			ExpressionsAPI:   d.Expressions.API(),
			queues:           p.Queues,
			blobs:            b.Objects,
		},
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
	worker := workerapi.NewStrictHandlerWithOptions(
		d.Workers.Protocol(),
		nil,
		workerapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  problem.RequestError,
			ResponseErrorHandlerFunc: problem.ResponseError,
		},
	)
	workerapi.HandlerWithOptions(worker, workerapi.ChiServerOptions{
		BaseURL:          WorkerBaseURL,
		BaseRouter:       r,
		ErrorHandlerFunc: problem.RequestError,
	})
	return r
}

// byPrefix authenticates a request under OperatorBaseURL with operator,
// one under WorkerBaseURL with worker and every other with api; a nil one
// refuses every credential.
type byPrefix struct{ api, operator, worker accessapi.Authenticator }

func (b byPrefix) Middleware(
	access func(*http.Request) accessapi.Access,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		api := orRefuse(b.api).Middleware(access)(next)
		operator := orRefuse(b.operator).Middleware(access)(next)
		worker := orRefuse(b.worker).Middleware(access)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch p := routePath(r); {
			case under(p, OperatorBaseURL):
				operator.ServeHTTP(w, r)
			case under(p, WorkerBaseURL):
				worker.ServeHTTP(w, r)
			default:
				api.ServeHTTP(w, r)
			}
		})
	}
}

// under reports whether path is base or below it, so /worker/v10 is not
// under /worker/v1.
func under(path, base string) bool {
	return path == base || strings.HasPrefix(path, base+"/")
}

func orRefuse(a accessapi.Authenticator) accessapi.Authenticator {
	if a == nil {
		return refuseAll{}
	}
	return a
}
