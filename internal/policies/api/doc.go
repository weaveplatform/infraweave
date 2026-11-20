// Package api publishes policy events and the account-qualified evaluator and
// identity ports. Evaluations distinguish policy-free behavior from preamble
// defaults, and return typed decisions with policy-owned receipt evidence.
// Changed identifies committed policy changes without retaining source text.
//
// # Input and output
//
// This package declares interfaces, input/output types and event JSON Schema;
// their implementations belong to the policies domain's private packages.
//
// # Dependencies
//
// The standard library and internal/kernel. The policies assembly registers
// event kinds and supplies the implementation ports to app.
package api
