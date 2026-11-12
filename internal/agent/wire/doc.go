// Package wire is the generated Go client of api/worker.openapi.yaml, the
// worker API a launcher calls under /worker/v1.
//
// client.gen.go is written by oapi-codegen from
// api/oapi-client-worker.yaml (make generate): the request and response
// models, Client with one method per operation, and ClientWithResponses,
// which decodes each response into its typed form. make verify-generate
// fails when it is stale; this file is the only one written by hand.
//
// ref: decisions 0004 and 0008
//
// # Input and output
//
// Each call sends one HTTP request to the server URL the caller gives
// NewClient, through the caller's HTTP client, and reads its response. It
// holds no credentials and retries nothing: the caller signs the
// registration and sets the worker session token through a request
// editor.
//
// # Dependencies
//
// It imports oapi-codegen's runtime and the standard library, and no
// server package, so the launcher's package graph stays free of server
// code. It is excluded from the coverage gate.
package wire
