// Package approval evaluates state-scoped approval policies using owner facts.
//
// ref: decision 0008
// ref: decision 0009
// ref: decision 0011
//
// # Input and output
//
// Evaluate consumes a run at its current state version and returns the combined
// APPROVAL result. Typed input contains owner-backed stack metadata, immutable
// runtime values, structural changes and the latest review per author for that
// exact state/version. Prior reviews remain in the older group.
//
// # Effects
//
// Evaluation joins the caller's transaction, fences the run and records its
// aggregate, receipts and policy-owned flags. It does not drive transitions.
// Reconciler consumes owner changes through durable bounded scan jobs, then
// acquires space, stack and run fences before reevaluating waiting runs.
//
// # Dependencies
//
// Public access, configuration, policy, stack and worker APIs; runs lifecycle,
// review, evidence and policy-input contracts; transaction and queue platforms.
// PostgreSQL implements persistence and objects opens sealed worker artifacts.
package approval
