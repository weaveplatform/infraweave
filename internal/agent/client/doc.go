// Package client is the launcher's and the runner's side of the worker
// API: Client calls register, poll, deregister, a run's specification and
// its reports through the generated internal/agent/wire client, setting
// each call's bearer token, and answers an *Error with the status and
// problem detail of any response other than the one the call expects.
// Status reads an error's status. Base is the worker API's base URL under
// the server's public URL a pool token names.
//
// ref: decision 0008 § "The worker API"
//
// # Input and output
//
// Each call sends one HTTP request through the caller's
// wire.HttpRequestDoer and reads its response; it retries nothing.
// Nothing is logged.
//
// # Dependencies
//
// It imports internal/agent/wire and the standard library. The launcher
// and the runner call it.
package client
