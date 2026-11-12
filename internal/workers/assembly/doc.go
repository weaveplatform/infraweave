// Package assembly constructs the workers domain, the private worker
// pools and their workers, after access. API is its partial of the API's
// strict server, aliased WorkersAPI; Stream is its migration stream,
// which internal/app registers after access'; Held, Keys and
// SealedColumns are its columns and rows for the orphan count and its
// columns for the key rotation; SpaceGuard is its say in space deletes
// and moves; SpaceOf finds the space the access domain decides a pool's
// operations on; Pools is the workersapi.Pools internal/app hands the
// stacks domain, GuardPools registers the stacks domain's
// workersapi.PoolGuard and BindStacks its SpaceOf; Protocol is the worker
// API's strict server, which internal/app mounts behind the controls of
// every API surface, WatchPolls wakes its held polls
// from infraweave_runs and StopPolls answers them as the replica shuts
// down; Settings are worker.poll_timeout_max; Purge deletes a deleted
// account's runner images, pools, workers and nonces PurgeBatch rows at a
// time, which internal/app runs for each accessapi.Deleted.
//
// # Input and output
//
// None.
//
// # Dependencies
//
// It imports the domain's http, pg, protocol, images, pool and worker
// packages, the access and workers api packages, internal/kernel,
// platform/crypt, the generated worker server, platform/sql's integrity,
// migrate and notify packages, and platform/tx. Only internal/app
// and cmd/ import it, as the constructors rule enforces.
package assembly
