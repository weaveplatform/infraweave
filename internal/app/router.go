package app

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
)

// BaseURL is the API's prefix, the server URL of api/api.openapi.yaml.
const BaseURL = "/api/v1"

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

// Blobs is object storage as the API role holds it: records written
// through an objectstore.Writer, which has no Delete, so serve can never
// delete an object (decision 0005), and the fs driver's /blobs/{token}
// routes, or nil for another driver.
type Blobs struct {
	Objects *blobs.Objects
	Routes  func(chi.Router)
}

var _ api.StrictServerInterface = apiServer{}

// API is the API listener's handler: the API under BaseURL behind c's rate
// limits and idempotency keys, whose state g holds, with each domain's
// operations from d, queue depths read from queues and objects in b, and the fs driver's signed-URL routes
// beside it. It serves no probe; those are the admin listener's alone,
// and are never rate-limited.
func API(
	log *slog.Logger,
	c config.Server,
	g Guards,
	d Domains,
	queues QueueStats,
	b Blobs,
) http.Handler {
	r := Guarded(log, c, apiOperations(), g, d.Auth)
	if b.Routes != nil {
		b.Routes(r)
	}
	strict := api.NewStrictHandlerWithOptions(
		apiServer{AccessAPI: d.Access.API(), queues: queues, blobs: b.Objects},
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
	return r
}
