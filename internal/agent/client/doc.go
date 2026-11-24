// Package client is the launcher's and the runner's side of the worker
// API: Client calls register, poll, deregister, the run token keys, read
// with at most MaxRunTokenKeys of their answer, a run's specification,
// its reports, its upload declarations, its log chunks' and artifacts'
// commits, its saved workspace's location and the reissue of its OIDC
// federation token through the generated
// internal/agent/wire client, setting each call's bearer token, and
// answers an *Error with the status and problem detail of any response
// other than the one the call expects. Workspace answers the redirect's
// location without following it, so the bearer token never reaches the
// store. Put and PutFile upload to a pre-signed URL an upload declaration
// answered, with no bearer token, PutFile streaming a file of any size.
// Status reads an error's status. A store's 400 or 403 to a pre-signed
// URL, RefusesSignature's rule, is marked ErrSigned (Signed), which every
// signed transfer of the launcher and the runner retries with a URL
// signed afresh. Base is the worker API's base URL under the server's
// public URL a pool token names.
//
// ref: decision 0008 § "The worker API"
//
// # Input and output
//
// Each call sends one HTTP request through the caller's
// wire.HttpRequestDoer and reads its response; it retries nothing. Put
// and PutFile send their bodies to an object store, PutFile reading a
// file.
// Nothing is logged.
//
// # Dependencies
//
// It imports internal/agent/wire and the standard library. The launcher
// and the runner call it.
package client
