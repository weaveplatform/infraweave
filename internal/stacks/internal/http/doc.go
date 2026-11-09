// Package http is the stacks domain's HTTP adapter: Server, the domain's
// partial of the API's strict server, which serves the Stacks operations
// that create, read, list, change, reslug, disable, enable, lock, unlock,
// star and delete a stack. The route table has already authorized each
// request on the stack's space, or on the space its body names; Server
// answers a stack in its space's slug, with the actions the caller holds
// there, and the members later phases own at their empty values. The
// service's errors answer as problems: a missing action 403 naming it, a
// stack locked by another 409 resource-locked naming the holder, a taken
// slug 409 already-exists, and a missing stack or space 404.
//
// # Input and output
//
// None of its own: it serves the requests the route table hands it.
//
// # Dependencies
//
// It imports accessapi, stacksapi, the domain's stacks package,
// internal/iac, internal/kernel, kernel/search, platform/gen/api and
// platform/httpkit/problem. The stacks domain's assembly constructs it.
package http
