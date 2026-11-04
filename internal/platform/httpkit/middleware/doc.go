// Package middleware is the HTTP middleware every infraweave listener shares.
//
// NewRouter is a chi router whose own errors are problem details: it assigns
// a request ID, recovers panics, runs the guards it is given, and answers an
// unknown path with NotFound (404) and an unserved method with
// MethodNotAllowed (405). Recover turns a panic into a 500 problem whose
// detail reveals nothing, and re-raises http.ErrAbortHandler. Decision
// 0003's rate limits and idempotency keys are its subpackages.
//
// # Input and output
//
// It reads requests and writes problem responses. A recovered panic is logged
// with its request ID through the given slog.Logger; nothing else is logged.
//
// # Dependencies
//
// It imports chi and chi's middleware for the router and request IDs, and
// platform/httpkit/problem for the bodies. internal/app and platform/observe
// import it.
package middleware
