package middleware

import (
	"net/http"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// DefaultMaxBody is the default of server.max_body_bytes, 1 MiB.
//
// ref: decision 0003 § "API surfaces"
const DefaultMaxBody = 1 << 20

// LimitBodies bounds the body of every request but those exempt names to
// limit bytes. A Content-Length over limit answers 413 payload-too-large
// at once, before any of the body is read. Any other body, chunked or of a
// length within it, reads as an *http.MaxBytesError once past limit, which
// problem.RequestError and problem.BodyError answer with the same 413, so
// no control or handler behind it buffers more than limit bytes.
func LimitBodies(limit int64, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.Body == http.NoBody || exempt(r) {
				next.ServeHTTP(w, r)
				return
			}
			if r.ContentLength > limit {
				problem.Write(w, problem.TooLarge(limit))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
