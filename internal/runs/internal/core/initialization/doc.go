// Package initialization evaluates server INITIALIZATION policies before a
// READY run is assigned to a worker.
//
// ref: decision 0009
//
// # Input and output
//
// Evaluate takes the fenced current run and returns the shared policy decision.
// Its lazy input builder uses stored run snapshots and actual stack metadata.
// It does not construct launcher policy input or a worker JWT.
//
// # Effects
//
// Evaluation and run-owned policy evidence join the caller's transaction after
// space, stack and run fencing. Claims owns the FAILED or PREPARING transition.
// Operational errors roll back; policy failures remain recorded decisions.
//
// # Dependencies
//
// Runs lifecycle, evidence and policy input projections, the transaction platform
// and the public policy and stack APIs. No worker-side exec builtin is enabled.
package initialization
