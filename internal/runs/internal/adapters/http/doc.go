// Package http is the runs domain's HTTP adapter: Server, the domain's
// partial of the API's strict server. It serves runTrigger through the
// trigger service, answering a locked stack 403 forbidden, a disabled
// stack or one with no commit to run 409 invalid-state, and a run as the
// API's Run with its history oldest first. The domain's other operations
// answer 501, or answer from the stacks domain's partial where it serves
// them empty. Its tests hold internal/kernel's run states, run types,
// stack states and run priorities to the API's enums.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports the generated API, platform/httpkit/apidelta,
// platform/httpkit/problem, stacksapi,
// internal/kernel and the domain's lifecycle and trigger. The runs
// domain's assembly constructs it.
package http
