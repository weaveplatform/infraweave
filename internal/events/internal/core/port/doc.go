// Package port declares persistence and metric contracts for event sink fan-out.
//
// # Input and output
//
// Delivery carries the account-scoped event, sink, target key, rule index and
// creation time. Store records a new identity in the caller’s transaction and
// bounds prune and purge batches. Metrics records committed outcomes.
//
// # Dependencies
//
// It imports only the standard library and internal/kernel. The delivery core
// consumes these interfaces and the adapters implement them.
package port
