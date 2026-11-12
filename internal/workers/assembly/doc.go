// Package assembly constructs the workers domain, the private worker
// pools and their workers, after access. API is its partial of the API's
// strict server, aliased WorkersAPI; Stream is its migration stream,
// which internal/app registers after access'; Held and SealedColumns are
// its columns for the orphan count and the key rotation; SpaceGuard is
// its say in space deletes and moves; SpaceOf finds the space the access
// domain decides a pool's operations on; Purge deletes a deleted
// account's pools, workers and nonces PurgeBatch rows at a time, which
// internal/app runs for each accessapi.Deleted.
//
// # Input and output
//
// None.
//
// # Dependencies
//
// It imports the domain's http, pg and pool packages, the access api
// package, internal/kernel, platform/crypt, platform/sql/integrity and
// migrate, and platform/tx. Only internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
