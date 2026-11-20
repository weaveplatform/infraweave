// Package http serves policy management, account and inherited-space searches,
// runtime metadata and contract publication on the shared API listener.
//
// # Input and output
//
// Routes exposes GET /.well-known/policy-contract.json as JSON Schema. The
// shared listener requires an account credential and the policies capability.
// Strict handlers expose persisted policy CRUD, the shared merge patch, full
// list views, reference search edges and scoped search suggestions. Policy
// views retain immutable ULIDs and Unix-second timestamps. These handlers do
// not provide attachment mutation, evaluation or saved policy samples.
//
// # Effects
//
// The contract is reflected once and its immutable bytes reused. Failures use
// the shared problem format. Listener middleware applies no-store, body bounds,
// account authorization, feature gates, rate limits and the caller switch.
// Management handlers delegate transactional writes and compilation to core.
//
// # Dependencies
//
// The standard library, chi, access/api, internal/kernel and its search package,
// generated API types, platform/httpkit's apisearch, problem and strict packages,
// platform/rego, and policies/internal/core and policies/internal/inputs.
package http
