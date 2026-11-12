// Package pg is the runs domain's PostgreSQL storage: Stream, its
// migration stream, whose runs table keys each run by its account and
// ULID, with the stack it runs on, its type, state, state_version and
// commit; run_state_transitions holds each transition a run went through,
// stack_run_locks the run that holds each stack's run lock,
// stack_run_state each stack's tracked run and stack_outputs the outputs
// of its last applied run. No table carries a foreign key to a stack,
// since stacks belong to the stacks domain. Store reads the run that
// holds each of some stacks' run lock, deletes a deleted stack's runs,
// with their transitions, and its run lock, tracked run and outputs, and
// purges a deleted account's rows a batch a statement.
// It is also the lifecycle's store: it locks a run's row, moves a run and
// records its transition, inserts a run, finds a stack's run of a type and
// commit that has not ended, makes a run its stack's tracked run, releases
// the run lock a run holds, and reads a stack's state and blocked from its
// tracked run's transitions and its run lock. For the scheduler it reads
// a stack's queued runs, oldest first, and takes a stack's run lock for a
// run when no run holds it. For a worker's claim it reads and locks a
// pool's next run, skipping locked rows, records the run's worker, counts
// a pool's drift runs on workers under a per-pool advisory lock, and
// reports whether a worker holds a run; Announce notifies
// infraweave_runs that a run waits in its pool. For a run's handover it
// reads and locks a run by its ID alone, as a run token names it, and
// makes the run's artifact key once, sealed to its row in
// artifact_key_sealed, which SealedColumns names, and opens it on every
// later read. Held names the columns that hold the access domain's
// accounts and the stacks domain's stacks. Every method joins the
// transaction its context carries.
//
// # Input and output
//
// It reads and writes PostgreSQL through the querier it is given, and
// seals and opens the artifact key through the Sealer it is given.
//
// # Dependencies
//
// It imports pgx, workersapi, the domain's handover, lifecycle and
// schedule, internal/kernel, platform/crypt, platform/sql,
// platform/sql/integrity, platform/sql/migrate, platform/sql/notify and
// platform/tx. The runs domain's assembly constructs it over the
// platform's keyring.
package pg
