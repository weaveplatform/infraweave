// Package worker is the generated strict chi server of
// api/worker.openapi.yaml, the worker API infraweave serves under
// /worker/v1 to launchers alone.
//
// ref: decision 0008 § "The worker API"
//
// server.gen.go comes from oapi-codegen (api/oapi-server-worker.yaml):
// the request and response models, StrictServerInterface, the chi handler
// HandlerWithOptions, NewStrictHandlerWithOptions and the embedded spec
// GetSpec. unimplemented.gen.go comes from tools/stubgen: UnimplementedStrict,
// which answers a 501 problem for every operation until the workers
// domain overrides it. make generate rewrites both and make
// verify-generate fails when they are stale; this file is the only one
// written by hand.
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
// platform/httpkit/problem for the stubs' 501. The workers domain's
// protocol adapter implements it and internal/app mounts it; the
// generated code is excluded from the coverage gate.
package worker
