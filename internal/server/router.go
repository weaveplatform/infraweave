package server

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/weaveplatform/infraweave/internal/server/gen/deploy"
	"github.com/weaveplatform/infraweave/internal/server/problem"
)

// BaseURL is the deploy API's prefix, the server URL of
// api/deploy.openapi.yaml.
const BaseURL = "/api/v1"

// deployAPI is the deploy API's strict server. Every operation answers 501
// until a family overrides it.
type deployAPI struct {
	deploy.UnimplementedStrict
}

// API is the API listener's handler: the deploy API under BaseURL behind
// c's rate limits and idempotency keys. It serves no probe; those are the
// admin listener's alone, and are never rate-limited.
func API(log *slog.Logger, c Config) http.Handler {
	r := Guarded(log, c, deployOperations())
	strict := deploy.NewStrictHandlerWithOptions(deployAPI{}, nil, deploy.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  problem.RequestError,
		ResponseErrorHandlerFunc: problem.ResponseError,
	})
	deploy.HandlerWithOptions(strict, deploy.ChiServerOptions{
		BaseURL:          BaseURL,
		BaseRouter:       r,
		ErrorHandlerFunc: problem.RequestError,
	})
	return r
}

// Admin is the admin listener's handler. GET /livez answers 200 while the
// process serves HTTP; decision 0017's readiness and startup probes join it
// when there is something to be ready for.
func Admin(log *slog.Logger) http.Handler {
	r := newRouter(log)
	r.Get("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return r
}

// newRouter is a chi router whose own errors are problem details. guards
// run after the request ID and recovery, so a panic in one is answered too.
func newRouter(log *slog.Logger, guards ...func(http.Handler) http.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, recoverProblem(log))
	r.Use(guards...)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		problem.Write(w, problem.New(http.StatusNotFound, "No route serves "+req.URL.Path+"."))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		problem.Write(w, problem.New(http.StatusMethodNotAllowed,
			req.Method+" is not served for "+req.URL.Path+"."))
	})
	return r
}

// recoverProblem turns a panicking handler into a 500 problem with a generic
// detail, so nothing about the failure reaches the client, and logs it with
// the request ID. http.ErrAbortHandler is re-raised: it is net/http's signal
// to drop the connection, not a failure.
func recoverProblem(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer answerPanic(log, w, r)
			next.ServeHTTP(w, r)
		})
	}
}

// answerPanic is deferred by recoverProblem; recover works because it is the
// deferred call itself.
func answerPanic(log *slog.Logger, w http.ResponseWriter, r *http.Request) {
	v := recover()
	if v == nil {
		return
	}
	if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
		panic(v)
	}
	log.ErrorContext(r.Context(), "handler panicked",
		"panic", v, "request_id", middleware.GetReqID(r.Context()))
	problem.Internal(w)
}
