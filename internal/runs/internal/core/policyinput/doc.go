// Package policyinput materializes run facts for the policy-owned typed builder.
//
// ref: decision 0009
//
// # Input and output
//
// Read returns the fenced current run and its immediately preceding account/
// stack run with committed flag snapshots. Facts and StructuralChanges convert
// persisted runtime settings and structural metadata into explicit public DTOs.
// Unknown facts remain absent; winning environment values must be supplied by
// their owner rather than inferred from key-only persisted configuration.
//
// # Effects
//
// Read joins the caller's transaction and locks the current run. Conversion is
// pure. No function evaluates a gate, changes state or copies flag ownership.
//
// # Dependencies
//
// The kernel, transaction platform, IaC structural types, runs lifecycle and
// evidence contracts, and public policies/stacks APIs.
package policyinput
