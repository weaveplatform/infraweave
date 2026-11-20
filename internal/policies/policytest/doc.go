// Package policytest exposes policy input builders to cross-domain acceptance
// tests. It adds no evaluator or production HTTP endpoint.
//
// ref: decision 0009
//
// # Input and output
//
// PlanVendor accepts an already opened worker artifact and returns its typed
// vendor policy blocks, retaining resource-change metadata and sensitivity masks.
//
// # Effects
//
// It reads the supplied reader and performs no persistence or evaluation.
//
// # Dependencies
//
// The standard library and the policies input package.
package policytest
