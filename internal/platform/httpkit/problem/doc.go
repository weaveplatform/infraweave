// Package problem writes RFC 9457 problem details, the body of every error
// response the API sends (the Problem schema in
// api/src/api/components-common.yaml), and adapts handler errors to them.
//
// Type is a problem type of the register docs/data/problems.json, its slug,
// status and title; a variable declares each slug, and Types lists them all.
// New and Type.New build a Problem, and Write sends one.
// RequestError and ResponseError are the strict server's error handlers:
// an *Error answers with the problem it carries, a NotImplementedError with a
// 501 naming the operation, and anything else with Internal's generic 500.
//
// # Input and output
//
// It writes the status, Content-Type application/problem+json and the JSON
// body to the response writer it is given. It reads nothing and logs
// nothing; TestUnitProblemRegistry holds Types to the register in both directions.
//
// # Dependencies
//
// It imports only the standard library. Every HTTP package imports it: the
// generated server's stubs, the middleware, the page codec, internal/app and,
// from P3, each domain's HTTP adapter.
package problem
