// Package operator is the generated strict chi server of
// api/operator.openapi.yaml, the operator API infraweave serves under
// /operator/v1 to operator principals alone.
//
// ref: decision 0021
//
// server.gen.go comes from oapi-codegen (api/oapi-server-operator.yaml):
// the request and response models, StrictServerInterface, the chi handler
// HandlerWithOptions, NewStrictHandlerWithOptions and the embedded spec
// GetSpec. unimplemented.gen.go comes from tools/stubgen: UnimplementedStrict,
// which answers a 501 problem for every operation until a domain overrides
// it. make generate rewrites both and make verify-generate fails when they
// are stale; this file is the only one written by hand.
//
// # Input and output
//
// The handler decodes HTTP requests (path, query, headers and JSON bodies)
// and writes JSON responses through the strict server it wraps. GetSpec
// inflates the spec compiled into the binary; nothing is read from disk.
//
// # Dependencies
//
// It imports chi, kin-openapi and oapi-codegen's runtime, and
// platform/httpkit/problem for the stubs' 501. internal/app composes it; the
// generated code is excluded from the coverage gate.
package operator
