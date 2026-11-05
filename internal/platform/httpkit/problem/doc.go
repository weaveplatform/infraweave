// Package problem writes RFC 9457 problem details, the body of every error
// response the API sends (the Problem schema in
// api/src/api/components-common.yaml), and adapts handler errors to them.
//
// Type is a problem type of the register docs/data/problems.json, its slug,
// status and title; a variable declares each slug, and Types lists them all.
// Type.New builds a Problem, and Write sends one, with decision 0022's
// instance, urn:infraweave:request: and the X-Request-Id the response
// carries. Failure answers an unexpected error: Unavailable's 503 with
// Retry-After for kernel.ErrUnavailable, otherwise Internal's generic 500.
// RequestError and ResponseError are the strict server's error handlers:
// an *Error answers with the problem it carries, a NotImplementedError with a
// 501 naming the operation, and anything else through Failure.
//
// # Input and output
//
// It writes the status, Content-Type application/problem+json, Retry-After
// on a 503 and the JSON body to the response writer it is given, and reads
// the X-Request-Id already set on it. It logs nothing; TestUnitProblemRegistry holds Types to the register in both directions.
//
// # Dependencies
//
// It imports the standard library and internal/kernel, for ErrUnavailable.
// Every HTTP package imports it: the
// generated server's stubs, the middleware, the page codec, internal/app and,
// from P3, each domain's HTTP adapter.
package problem
