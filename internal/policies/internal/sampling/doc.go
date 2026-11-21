// Package sampling owns sealed evaluation retention and durable upload handoff.
//
// ref: decision 0009
//
// # Input and output
//
// Record receives the actual per-policy input, source and result from the
// evaluator. It records safe metadata for every evaluation, and reserves an
// account/hour budget before staging a requested sample. Only published objects
// are readable; a request or pending upload is not evidence of retention.
//
// # Effects
//
// Evaluation metadata, budget reservation, encrypted staging and the ID-only
// upload job join the evaluation transaction. Upload releases the connection
// before streaming a sealed object. Publication uses a new fenced transaction.
// An immutable key is uploaded at most once; ambiguous partial failure is
// unretained, while a completed upload can recover publication without rewriting.
//
// # Dependencies
//
// Policy public contracts, the transaction and queue platform, crypt values and
// the sealed object-store owner. No function writes run receipts or flags.
package sampling
