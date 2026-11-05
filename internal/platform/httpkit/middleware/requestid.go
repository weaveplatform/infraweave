package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// RequestIDHeader carries the request ID both ways (decision 0017).
const RequestIDHeader = "X-Request-Id"

// validID is the X-Request-Id a client may choose; any other is replaced, so
// a log line never carries a client's control characters or a megabyte.
var validID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// idEncoding writes 128 random bits as 26 base32 characters.
var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type requestIDKey struct{}

// RequestID gives each request an ID: the client's X-Request-Id when it is
// valid, otherwise a new random one. The ID is echoed in the response
// header and carried on the context for RequestIDFrom.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails
	return idEncoding.EncodeToString(b[:])
}

// RequestIDFrom is the ID RequestID gave the request ctx belongs to, or ""
// outside a request.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// AccessLog logs one info line per request once it completes: the method,
// the chi route pattern rather than the raw path, so IDs in paths do not
// split one route into many, the status, the bytes written, the duration,
// the client address and user agent. Bodies are never logged. It runs as a
// guard inside Recover, so a handler that panics is logged as the 500
// Recover answers.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			finished := false
			defer func() { logRequest(log, r, ww, start, finished) }()
			next.ServeHTTP(ww, r)
			finished = true
		})
	}
}

func logRequest(
	log *slog.Logger,
	r *http.Request,
	ww middleware.WrapResponseWriter,
	start time.Time,
	finished bool,
) {
	status := ww.Status()
	switch {
	case !finished:
		status = http.StatusInternalServerError
	case status == 0:
		status = http.StatusOK
	}
	route := ""
	if rc := chi.RouteContext(r.Context()); rc != nil {
		route = rc.RoutePattern()
	}
	log.InfoContext(r.Context(), "request",
		"method", r.Method,
		"route", route,
		"status", status,
		"bytes", ww.BytesWritten(),
		"duration_ms", time.Since(start).Milliseconds(),
		"remote_addr", r.RemoteAddr,
		"user_agent", r.UserAgent())
}
