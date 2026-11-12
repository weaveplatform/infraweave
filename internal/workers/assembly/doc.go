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
// every API surface, BindScheduler binds the runs domain's
// workersapi.Scheduler its polls claim runs through, WatchPolls wakes its
// held polls from infraweave_runs, all of a pool's or, for a waiting run,
// one, and StopPolls answers them as the replica shuts down;
// BindLiveness binds the runs domain's workersapi.Liveness; Cron is
// worker.reap, every 30 seconds on the scheduler queue, which marks
// silent workers offline, deletes lost ones and fails their runs, and
// fails overdue preparations, and worker.sample, every minute on the same
// queue, which writes each pool's sample of its workers and queued runs,
// counted through the bound scheduler, and deletes the expired ones;
// Samples is the workersapi.Samples other domains read them through;
// Settings are worker.poll_timeout_max, worker.offline_after and
// worker.samples.retention; Purge deletes a deleted account's pool
// samples, runner images, pools, workers and nonces PurgeBatch rows at a
// time, which internal/app runs for each accessapi.Deleted.
//
// # Input and output
//
// None.
//
// # Dependencies
//
// It imports the domain's http, pg, protocol, images, pool, sample and
// worker packages, the access and workers api packages, internal/kernel,
// platform/cron, platform/crypt, platform/queue, the generated worker
// server, platform/sql's integrity, migrate and notify packages, and
// platform/tx. Only internal/app
// and cmd/ import it, as the constructors rule enforces.
package assembly
