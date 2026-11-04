package middleware

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// NewRouter is a chi router whose own errors are problem details. guards
// run after the request ID and recovery, so a panic in one is answered too.
func NewRouter(log *slog.Logger, guards ...func(http.Handler) http.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, Recover(log))
	r.Use(guards...)
	r.NotFound(NotFound)
	r.MethodNotAllowed(MethodNotAllowed)
	return r
}

// NotFound answers a path no route serves with a 404 problem.
func NotFound(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, problem.New(http.StatusNotFound, "No route serves "+r.URL.Path+"."))
}

// MethodNotAllowed answers a method a route does not serve with a 405
// problem.
func MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, problem.New(http.StatusMethodNotAllowed,
		r.Method+" is not served for "+r.URL.Path+"."))
}

// Recover turns a panicking handler into a 500 problem with a generic
// detail, so nothing about the failure reaches the client, and logs it with
// the request ID. http.ErrAbortHandler is re-raised: it is net/http's signal
// to drop the connection, not a failure.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer answerPanic(log, w, r)
			next.ServeHTTP(w, r)
		})
	}
}

// answerPanic is deferred by Recover; recover works because it is the
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
