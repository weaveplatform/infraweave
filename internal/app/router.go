package app

import (
	"log/slog"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// BaseURL is the API's prefix, the server URL of api/api.openapi.yaml.
const BaseURL = "/api/v1"

// apiServer is the API's strict server. Each domain's partial embeds here
// under its domain's name; until one overrides an operation, it answers 501
// through UnimplementedStrict.
type apiServer struct {
	api.UnimplementedStrict
}

var _ api.StrictServerInterface = apiServer{}

// API is the API listener's handler: the API under BaseURL behind c's rate
// limits and idempotency keys. It serves no probe; those are the admin
// listener's alone, and are never rate-limited.
func API(log *slog.Logger, c config.Server) http.Handler {
	r := Guarded(log, c, apiOperations())
	strict := api.NewStrictHandlerWithOptions(apiServer{}, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  problem.RequestError,
		ResponseErrorHandlerFunc: problem.ResponseError,
	})
	api.HandlerWithOptions(strict, api.ChiServerOptions{
		BaseURL:          BaseURL,
		BaseRouter:       r,
		ErrorHandlerFunc: problem.RequestError,
	})
	return r
}
