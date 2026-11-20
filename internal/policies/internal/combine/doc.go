// Package combine resolves independent policy receipts into deterministic,
// typed gates and directives. Policy-free targets retain an explicit outcome;
// attached preamble defaults and program failures have per-type behavior.
// Rejections, denies and ignores take their declared precedence, ownership of
// flags remains on each receipt, and conflicts retain safe failure metadata.
//
// # Input and output
//
// Functions take immutable program receipts and return owned decision evidence
// in stable policy-ID order. They perform no storage, provider or sampling IO.
//
// # Dependencies
//
// The standard library, policies/api and the policies domain's typed inputs.
//
// ref: decision 0009
package combine
