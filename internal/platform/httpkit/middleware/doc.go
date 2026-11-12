// Package middleware is the HTTP middleware every infraweave listener shares.
//
// NewRouter is a chi router whose own errors are problem details: it assigns
// a request ID, recovers panics, runs the guards it is given, and answers an
// unknown path with NotFound (404) and an unserved method with
// MethodNotAllowed (405). RequestID keeps a client's X-Request-Id of 1 to
// 128 characters of [A-Za-z0-9._-], replaces any other with 128 random bits
// in base32, echoes it, and carries it on the context for RequestIDFrom.
// Recover turns a panic into a 500 problem whose detail reveals nothing, and
// re-raises http.ErrAbortHandler. AccessLog logs one line per request.
// LimitBodies bounds every request body to server.max_body_bytes
// (DefaultMaxBody by default) but those of the routes it exempts: a larger
// Content-Length answers 413 payload-too-large at once, and any other body
// stops at the bound with an *http.MaxBytesError, which problem answers
// with the same 413.
// The API's rate limits and idempotency keys are its subpackages,
// ratelimit and idempotency.
//
// ref: decision 0003
//
// # Input and output
//
// It reads requests, wraps a bounded request's body, and writes problem
// responses and the X-Request-Id header. AccessLog logs the method, the chi route pattern, the status, the
// bytes written, the duration, the client address and the user agent at
// info, never a body; a recovered panic is logged with its request ID. Both
// go through the given slog.Logger.
//
// # Dependencies
//
// It imports chi and chi's middleware for the router and the response
// writer that counts bytes, and platform/httpkit/problem for the bodies.
// internal/app and platform/observe import it.
package middleware
