// Package http serves the policy contract on the shared API listener.
//
// # Input and output
//
// Routes exposes GET /.well-known/policy-contract.json as JSON Schema. The
// shared listener requires an account credential and the policies capability.
// It returns schema only, never stored policy inputs or samples.
//
// # Effects
//
// The contract is reflected once and its immutable bytes reused. Failures use
// the shared problem format. Listener middleware applies no-store, body bounds,
// account authorization, feature gates, rate limits and the caller switch.
//
// # Dependencies
//
// The standard library, chi, platform/httpkit/problem and policies/internal/inputs.
package http
