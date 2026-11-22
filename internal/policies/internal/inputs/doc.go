// Package inputs defines policy input snapshots, raw decisions and their
// published JSON Schema. Its field names follow the recorded policy contract and
// input documents; only the spacelift namespace becomes infraweave.
//
// ref: report 09 §4; decision 0009
//
// # Input and output
//
// Inputs and decisions retain absent, null and zero values separately. Contract
// produces the schema from these types. Optional input envelopes retain their
// absence when the caller does not supply them. Object marks only provider or
// plugin-owned JSON documents whose keys are outside the policy contract.
//
// # Effects
//
// This package performs no I/O, evaluation or persistence.
//
// # Dependencies
//
// The standard library only.
package inputs
