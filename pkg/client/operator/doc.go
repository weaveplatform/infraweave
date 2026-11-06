// Package operator is the generated Go client of api/operator.openapi.yaml,
// the operator API infraweave serves under /operator/v1 (decision 0021).
//
// client.gen.go is written by oapi-codegen from
// api/oapi-client-operator.yaml (make generate): the request and response
// models, Client with one method per operation, and ClientWithResponses,
// which decodes each response into its typed form. make verify-generate
// fails when it is stale; this file is the only one written by hand.
//
// # Input and output
//
// Each call sends one HTTP request to the server URL the caller gives
// NewClient, through the caller's HTTP client, and reads its response. It
// holds no credentials and retries nothing: the caller sets the operator
// token, which an operator key exchanges for at POST /api/v1/auth/token,
// through a request editor.
//
// # Dependencies
//
// It imports oapi-codegen's runtime and the standard library, and nothing
// under internal/, as nothing under pkg/ may (decision 0004). It is excluded
// from the coverage gate.
package operator
