// Package queue is decision 0011's job queue in PostgreSQL: background work
// that a change asks for in its own transaction and that runs after it
// commits, at least once, on any replica.
//
// A Kind names a job's kind, its logical queue (Queues) and the Policy its
// duplicates follow. EnqueueTx inserts a job inside the caller's
// transaction, so the job exists exactly when the change does; Enqueue runs
// it in a transaction of its own. A duplicate kind and unique key resolves
// by the kind's policy: Coalesce keeps one pending job with the newest
// payload, or marks a running one to run again; Ignore drops it.
//
// Claim takes due jobs of one queue with FOR UPDATE SKIP LOCKED, so
// replicas never wait on each other, in priority, run time and insertion
// order; jobs that share an ordering key run one at a time, oldest first.
// Each claim increments the job's generation, and every transition of the
// Job it returns (Extend, Complete, CompleteTx, Fail, Kill, Snooze, Release)
// holds only while that generation does, otherwise ErrLeaseLost. CompleteTx
// commits a handler's writes with its completion. Fail retries after
// Backoff, with full jitter, until the job's attempts run out and it is
// dead. Recover returns running jobs whose lease lapsed to pending. Prune
// deletes finished jobs past their retention; ListDead, Retry and Discard
// are the operator's dead-job operations, and Stats reports each queue's
// depth.
//
// A process that runs jobs registers a Handler per kind in a Registry, with
// its timeout (at most MaxTimeout), the feature that gates it and its retry
// delay, and runs Workers: a pool per logical queue with that queue's
// worker count (Config). An idle pool claims when a notification names its
// queue, when the listener resyncs, when one of its slots frees, or every
// poll interval; it claims only the kinds this process registered, so a
// newer kind waits for a replica that knows it. A running handler's lease is
// renewed every third of the lease, and its context ends with ErrLeaseLost
// once another claim takes the job. A nil result completes the job;
// Snooze, Permanent, any other error, a panic and the timeout map to
// decision 0011's outcomes. A job whose account's Entitlements lack its
// kind's feature completes as skipped without running. Run stops claiming
// when its context ends, lets handlers finish for the shutdown grace, then
// cancels them and hands their jobs back without counting the attempt.
//
// # Input and output
//
// It reads and writes the platform stream's jobs table through the pool or
// the transaction on the context, and sends pg_notify on infraweave_jobs
// with the job's queue in the enqueueing transaction, which PostgreSQL
// delivers at commit. Payloads are JSON of at most 64 KiB and carry IDs,
// never secrets. Database errors that mean PostgreSQL is unreachable are
// marked with kernel.ErrUnavailable. Workers log their start and stop at
// info, each attempt's outcome at debug, dead jobs, lost leases and
// recovered jobs at warn, and a handler's panic with its stack at error.
//
// # Dependencies
//
// It imports pgx, platform/tx, platform/sql for the unavailable error,
// platform/sql/notify for the wake-up channel and its messages, and
// internal/kernel for account IDs; Workers log through log/slog. Stores
// in every domain enqueue through it; queuetest holds its contract suite.
package queue
