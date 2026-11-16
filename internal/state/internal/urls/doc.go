// Package urls serves the state domain's signed URLs: Server is the
// domain's partial of the API's strict server, whose stateUploadUrl and
// stateDownloadUrl sign a token, and the handler of the routes the tokens
// open. A PUT to an upload URL stores one state file for import, sealed
// in the uploads store under the object ID and the principal that asked;
// a GET of a download URL streams the stack's version it was signed for,
// unsealed, while the stack keeps its state with external state access on.
//
// # Input and output
//
// It reads an upload's body as it streams and writes a download's; the
// state domain's core stores and opens them.
//
// # Dependencies
//
// It imports internal/kernel, accessapi, stacksapi, stateapi, the state
// domain's core and signed packages, the generated API and
// platform/httpkit. The state domain's assembly constructs it over the
// core, stacksapi.Stacks, the access domain's Authorizer and a
// signed.Signer over the installation's keyring.
package urls
