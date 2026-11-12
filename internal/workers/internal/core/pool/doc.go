// Package pool keeps an account's private worker pools: Service creates
// one in a space from a certificate signing request for an EC P-256 key,
// which the installation's pool CA, created on first use, signs under a
// new serial; reads, searches and suggests them, the spaces its caller
// may read keeping the rest out; replaces their settings, a move needing
// WORKER_POOL_CREATE on the new space; resets a pool's certificate under
// a new serial, which leaves registrations under the old one behind; and
// deletes a pool with its workers. Token is a pool's pool token and
// PreparingTimeout its effective preparing timeout. SpaceOf finds a
// pool's space for the access domain's middleware, and SpaceGuard
// refuses deleting a space a pool lives in.
//
// ref: decisions 0007 and 0008
//
// # Input and output
//
// New takes the Store, the pool its writes begin transactions on, the
// access domain's spaces and authorizer, the server's public URL and the
// installation's default preparing timeout.
//
// # Dependencies
//
// It imports the access api package, internal/kernel and its search
// package, platform/tx and the domain's credential package. The domain's
// assembly constructs it over the pg adapter.
package pool
