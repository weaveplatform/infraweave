// Package initpolicy compiles and evaluates the launcher's local initialization
// policy before fetching source, restoring a workspace or acquiring tools.
//
// # Input and output
//
// Load snapshots an optional local Rego file under fixed size/time bounds. Input
// projects only immutable assignment facts and the actual resolved image into
// the worker-only version-1 document. Current run metadata has no producer, so
// user_provided_metadata is an empty array. Evaluate returns bounded, masked
// print/deny text or a safe failure; no raw input or OPA diagnostic is logged.
// The policy's exec calls go to the rego.Commands the caller supplies for that
// evaluation alone; a call that produces no result fails the evaluation with the
// call's reason, which names no argument, output or image. The run token is
// delivered by the authenticated worker protocol; independent signature
// admission belongs to its separate verifier.
//
// # Dependencies
//
// It imports only the standard library, agent/wire, platform/mask and the shared
// bounded platform/rego engine. Worker queries have a separate cache identity and
// capability set; no server domain is linked, and the executor of exec calls is
// the caller's. The caller supplies an earlier preparation deadline through
// context; policy evaluation never extends it.
package initpolicy
