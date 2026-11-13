// Package backend serves the Terraform http backend's routes under
// /state/v1 for the run each request's run token names: LOCK and UNLOCK
// take and release a workspace's locks through the state domain's core,
// shared or exclusive as the runs domain's StateAccess grants the run on
// the stack, and GET and POST, once the grant allows them, answer 501
// until state versions are served. The stack segment is a stack's
// canonical ID or its slug in the run's account; a stack no account of
// the run's has, or whose state infraweave does not keep, answers 404. A
// lock refused for another lock answers 423 with the holder's LockInfo,
// which Terraform reports; every other failure is a problem document.
// Unserved adds the same routes answering 501, for an API built without
// the state domain.
//
// # Input and output
//
// It serves HTTP through the routes it adds to the router it is given,
// and reads LockInfo bodies of at most 16 KiB.
//
// # Dependencies
//
// It imports internal/kernel, runsapi, stacksapi, stateapi, the domain's
// core, chi, platform/httpkit/middleware, its ratelimit and
// platform/httpkit/problem. The state domain's assembly constructs it.
package backend
