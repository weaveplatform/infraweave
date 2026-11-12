// Package assembly constructs the runs domain over its Deps, after the
// configuration domain, with the lifecycle every run is created and moved
// through. Jobs is its run.schedule handler, which moves a stack's queued
// runs to READY, and Subscribe subscribes the consumer that schedules a
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
// the back edge the stack view reads each stack's blocker through;
// Scheduler is the workersapi.Scheduler internal/app binds in the workers
// domain, the back edge a worker's poll claims its pool's next run
// through and a run token's bearer reads its run's handover through;
// BindExpressions binds the registry the handover renders a stack's
// environment with; SealedColumns names the run's sealed artifact key, for
// internal/app's reseal; StackGuard is the
// stacksapi.StackGuard internal/app registers there, so a stack's delete
// deletes its runs in the same transaction; API is its partial of the
// API's strict server, aliased RunsAPI; Held declares the columns that
// hold the access domain's accounts and the stacks domain's stacks, for
// the nightly orphan count; Stream is its migration stream, which
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
// surface, trigger, http and pg packages, platform/crypt, platform/expr,
// platform/outbox, platform/queue, platform/sql/integrity and
// platform/sql/migrate. Only
// internal/app and cmd/ import it, as the constructors rule enforces.
package assembly
