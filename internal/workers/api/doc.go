// Package api is what other domains call of the workers domain: Pools,
// which holds an account's worker pool for a stack about to name it, when
// the stack's space reaches the pool; ErrNoPool when it does not. A domain
// whose rows name pools registers a PoolGuard, which a pool's delete and
// move ask for the rows that name it, as PoolUses.
//
// # Input and output
//
// None; it declares types and interfaces.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The stacks domain
// imports it and implements PoolGuard; the workers domain's assembly
// implements Pools.
package api
