// Package api is what other domains call of the workers domain: Pools,
// which holds an account's worker pool for a stack about to name it, when
// the stack's space reaches the pool; ErrNoPool when it does not. A domain
// whose rows name pools registers a PoolGuard, which a pool's delete and
// move ask for the rows that name it, as PoolUses. Scheduler is the back
// edge the runs domain implements: a worker's Claim of the run that has
// waited longest in its pool's first tier, answered as an Assignment, and
// the Handover of a claimed run, its Spec in clear and its secrets for
// the workers domain to seal to the pool's key; ErrNotOnWorker when the
// run is not on a worker. Waiting is the notification payload that wakes
// a held poll for a run.
//
// # Input and output
//
// None; it declares types and interfaces.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The stacks domain
// imports it and implements PoolGuard; the runs domain imports it and
// implements Scheduler; the workers domain's assembly implements Pools.
package api
