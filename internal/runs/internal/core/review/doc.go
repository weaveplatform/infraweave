// Package review owns immutable human and key reviews and their policy facts.
//
// ref: decision 0009
//
// # Input and output
//
// Record binds a decision and note to the admitted author, original metadata,
// run state and state version. Effective returns current and older approvals
// and rejections; the latest review per canonical author is the only effective
// vote for that state version. Distinct sessions cannot create extra people.
//
// # Effects
//
// Selection and validation are pure. Store scopes immutable history by account
// and joins the caller's transaction for writes. This package does not evaluate
// policies, authorize requests or transition runs.
//
// # Dependencies
//
// The kernel, public typed policy facts and the standard library. The runs
// PostgreSQL adapter implements Store; approval and read consume the records.
package review
