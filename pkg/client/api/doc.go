// Package api is the generated Go client of api/api.openapi.yaml, the API
// infraweave serves under /api/v1; the operator API's is client/operator.
//
// client.gen.go is written by oapi-codegen from api/oapi-client-api.yaml
// (make generate): the request and response models, Client with one method
// per operation, and ClientWithResponses, which decodes each response into
// its typed form. make verify-generate fails when it is stale; this file is
// the only one written by hand.
//
// ref: decisions 0001 and 0004
//
// # Input and output
//
// Each call sends one HTTP request to the server URL the caller gives
// NewClient, through the caller's HTTP client, and reads its response.
// It holds no credentials and retries nothing; pkg/sdk adds the token
// exchange, idempotency keys and retries over it.
//
// # Dependencies
//
// It imports oapi-codegen's runtime and the standard library, and nothing
// under internal/, as nothing under pkg/ may, so the public client never
// depends on project-only code. It is excluded from the coverage gate.
package api
