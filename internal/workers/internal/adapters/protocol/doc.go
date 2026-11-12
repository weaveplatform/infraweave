// Package protocol is the worker API's adapter: Server is the generated
// strict server of api/worker.openapi.yaml, registering, polling and
// deregistering workers through the worker package. internal/app mounts
// it behind the controls every API surface shares, whose authenticator
// admits worker session tokens alone and puts each one's bearer on the
// request's context, which Server hands the service. A poll's answer
// carries the run it claimed, its state and its run token. A credential the
// service refuses is logged with its reason and answered 401 without it,
// and a registration beyond the private-workers limit answers 403
// limit-reached.
//
// ref: decision 0008 § "The worker API"
//
// # Input and output
//
// The generated handler's request objects, from launchers; it answers
// response objects and problems, and logs refused credentials.
//
// # Dependencies
//
// It imports the access and features api packages, the generated worker
// server, platform/httpkit's problem package and the domain's worker
// package. The domain's assembly constructs it.
package protocol
