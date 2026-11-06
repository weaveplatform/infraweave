// Package sdk is infraweave's Go SDK, the supported way a Go program calls
// the API (decision 0001). New returns a Client whose API field is the
// generated client of every served operation (pkg/client/api), whose
// Operator field is the operator API's (pkg/client/operator) and whose Do
// sends any request; all three carry the API's conventions (decision
// 0003):
//
//   - a bearer token, set with WithToken, or exchanged for an API key with
//     WithAPIKey and exchanged again before it expires;
//   - an Idempotency-Key on every POST and PATCH, chosen once per call and
//     sent again on each of its retries;
//   - retries after a 429, or a 503 with Retry-After, honouring the wait
//     the server names, bounded by Retry and the caller's context;
//   - typed problem errors, one per slug of docs/data/problems.json
//     (decision 0022), tested with errors.As; Check makes one from an
//     answer of the generated client;
//   - Iterate, a Go iterator over a list operation that follows nextCursor.
//
// # Input and output
//
// It sends HTTP requests through the Doer it is given, http.DefaultClient
// by default, and returns the answers and their typed errors. It logs
// nothing; the only state it keeps between calls is WithAPIKey's current
// token.
//
// # Dependencies
//
// It imports the generated clients pkg/client/api and pkg/client/operator
// and the standard library, and nothing under internal/, so another repository can import
// it without the server.
package sdk
