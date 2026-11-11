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
// run when no run holds it. Held names the columns that hold the access domain's accounts and the
// stacks domain's stacks. Every method joins the transaction its context
// carries.
//
// # Input and output
//
// It reads and writes PostgreSQL through the querier it is given.
//
// # Dependencies
//
// It imports pgx, the domain's lifecycle and schedule, internal/kernel, platform/sql,
// platform/sql/integrity, platform/sql/migrate and platform/tx. The runs domain's assembly
// constructs it.
package pg
