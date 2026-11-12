// Package protocol is the worker API's adapter: Server is the generated
// strict server of api/worker.openapi.yaml, registering, polling and
// deregistering workers through the worker package. internal/app mounts
// it behind the controls every API surface shares, whose authenticator
// admits worker session tokens, and run tokens on the run specification's
// route alone, and puts each one's bearer on the request's context, which
// Server hands the service. A poll's answer carries the run it claimed,
// its state, its state version and its run token; GET /runs/{run}/spec
// answers the run's specification with its secrets sealed, 409
// invalid-state for a run not on a worker and 422 validation-failed for a
// template that does not render. A credential the service refuses is
// logged with its reason and answered 401 without it, and a registration
// beyond the private-workers limit answers 403 limit-reached.
//
// ref: decision 0008 § "The worker API", § "The run specification and its
// secrets"
//
// # Input and output
//
// The generated handler's request objects, from launchers; it answers
// response objects and problems, and logs refused credentials.
//
// # Dependencies
//
// It imports the access, features and workers api packages,
// internal/kernel, the generated worker server, platform/httpkit's problem
// package and the domain's worker package. The domain's assembly constructs it.
package protocol
