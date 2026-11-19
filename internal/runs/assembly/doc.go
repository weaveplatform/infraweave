// Package assembly constructs the runs domain over its Deps, after the
// configuration domain, with the lifecycle every run is created and moved
// through. Jobs is its run.schedule handler, which moves a stack's queued
// runs to READY, Cron its run.timers periodic task, which ends the runs
// nobody moves, and Subscribe subscribes the consumer that schedules a
// stack once it is unlocked or enabled. RegisterEvents registers
// runsapi.Updated with the outbox, and BindSources binds the vcs domain's
// runsapi.RunSources and stacksapi.StackSources, back edges, so a run's
// end releases its source credentials and a trigger resolves the commit
// it names. Surfaces adds run to the configuration domain's environment
// surface, for the expression registry internal/app builds. Purge deletes
// a deleted account's runs, transitions, run locks, tracked runs and
// outputs PurgeBatch rows at a time, as internal/app's runs.purge consumer
// of accessapi.Deleted. StackStatus
// is the stacksapi.StackStatus internal/app binds in the stacks domain,
// the back edge the stack view reads each stack's blocker through, and
// StackDestroyer the stacksapi.StackDestroyer it binds there, the back
// edge a stack's delete with destroyResources creates its destroy run
// through;
// Scheduler is the workersapi.Scheduler internal/app binds in the workers
// domain, the back edge a worker's poll claims its pool's next run
// through and a run token's bearer reads its run's handover through;
// Liveness is the workersapi.Liveness internal/app binds there, the back
// edge the reap fails lost runs and overdue preparations through and a
// poll fails a run whose container ended; Signals is the
// workersapi.Signals internal/app binds there, the back edge a busy
// worker's poll delivers its run's stop or kill through; Settings are run.lost_after and
// run.preparing_timeout_default, which internal/app hands the workers
// domain, and run.timers, which Deps carries with
// objectstore.retention.run-logs, which runLogRetentionDays answers;
// BindExpressions binds the registry the handover renders a stack's
// environment with; SealedColumns names the run's sealed artifact key, for
// internal/app's reseal; StackGuard is the
// stacksapi.StackGuard internal/app registers there, so a stack's delete
// deletes its runs in the same transaction; StateAccess is the
// runsapi.StateAccess internal/app hands the state domain, a forward edge
// that decides a run token's access to its stack's state from the run's
// current state, StackRuns the runsapi.StackRuns it hands there too,
// which lists a stack's runs that have not ended, and RunEnds the
// runsapi.RunEnds it hands there as well, which answers which of an
// account's runs have not ended; RunAccess is the accessapi.Runs internal/app binds in
// the access domain, the back edge an in-run API token's authentication
// reads its run's stack, space and liveness through; API is its partial
// of the
// API's strict server, aliased RunsAPI; Held declares the columns that
// hold the access domain's accounts and the stacks domain's stacks, and
// Keys the runs' key, which other domains' held columns name, for the
// nightly orphan count; CommitRuns supplies runsapi.CommitRuns, the account-bound
// identities and creation times of runs on a stack's requested commits;
// CheckFacts supplies minimal current state and plan facts for feedback, leaving
// historical transition cutoffs to HistoryAt and provider policy to its caller;
// PlanningLogs supplies an account-bound bounded tail of an exact planning
// transition's masked log, without full run views or signed URLs;
// Stream is its migration stream, which
// internal/app registers after configuration's.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL, the keyring, the stacks,
// spaces, environment and in-run tokens of other domains, the outbox and
// the queue through Deps.
//
// # Dependencies
//
// It imports runsapi, stacksapi, configapi, workersapi, accessapi,
// internal/kernel, the domain's core, handover, lifecycle, schedule,
// surface, timers, trigger, http and pg packages, platform/cron,
// platform/crypt, platform/expr,
// platform/outbox, platform/queue, platform/sql/integrity and
// platform/sql/migrate. Only
// internal/app and cmd/ import it, as the constructors rule enforces.
package assembly
