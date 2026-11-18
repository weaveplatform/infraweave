// Package delivery fans one outbox event out to its Commit checks targets.
// Records, feedback jobs and fenced queue completion commit together.
// Repeated fan-out cannot enqueue a second target, and metrics count
// committed outcomes only.
//
// # Input and output
//
// Consume takes the event and its claimed events.consume job. Prune and Purge
// remove expired or deleted-account records in bounded batches. The core
// emits no provider request and serves no HTTP operation.
//
// # Dependencies
//
// It imports the domain’s port contracts, vcsapi, internal/kernel and
// platform/outbox, queue and tx. Assembly binds its adapters; it imports none
// of them.
package delivery
