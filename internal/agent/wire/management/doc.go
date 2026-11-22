// Package management is the generated Go client of the account API
// operations the Kubernetes worker controller calls under /api/v1: the
// API key exchange, the caller's account, and the worker pool lookup by
// owner, create, update and delete.
//
// client.gen.go is written by oapi-codegen from
// api/oapi-client-controller.yaml (make generate), which selects those
// operations from api/openapi.yaml. make verify-generate fails when it
// is stale; this file is the only one written by hand.
//
// # Input and output
//
// Each call sends one HTTP request to the server URL the caller gives
// NewClientWithResponses, through the caller's HTTP client, and reads its
// response. It holds no credentials: the caller sets the bearer token
// through a request editor.
//
// # Dependencies
//
// It imports oapi-codegen's runtime and the standard library, and no
// server package. It is excluded from the coverage gate.
package management
