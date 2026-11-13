// Package backend serves the Terraform http backend's routes under
// /state/v1 for the run each request's run token names: LOCK and UNLOCK
// take and release a workspace's locks through the state domain's core,
// shared or exclusive as the runs domain's StateAccess grants the run on
// the stack. GET streams the workspace's current state, unsealed, with no
// Content-MD5, or answers 204 when it has none; POST writes a version
// under the exclusive lock its ID query parameter names, its body of a
// declared Content-Length of at most the handler's maxSize. Each read of
// a POST's body and write of a GET's answer moves the connection's
// deadlines through platform/httpkit/moving. The stack segment is a
// stack's canonical ID or its slug in the run's account; a stack no
// account of the run's has, or whose state infraweave does not keep,
// answers 404. A lock or a write refused for another lock answers 423
// with the holder's LockInfo, which Terraform reports, or {} for a write
// when no lock is held; every other failure is a problem document.
// Unserved adds the same routes answering 501, for an API built without
// the state domain.
//
// # Input and output
//
// It serves HTTP through the routes it adds to the router it is given:
// it reads LockInfo bodies of at most 16 KiB and streams state bodies
// both ways.
//
// # Dependencies
//
// It imports internal/kernel, runsapi, stacksapi, stateapi, the domain's
// core, chi, platform/httpkit/middleware, its ratelimit,
// platform/httpkit/moving and platform/httpkit/problem. The state
// domain's assembly constructs it.
package backend
