// Package assembly constructs the state domain over its Deps, after the
// runs domain: the core that opens a stack's workspace under the stack's
// hold and takes and releases its locks, over the domain's pg adapter,
// and the backend Routes adds to the API listener under /state/v1, which
// decides each request through the runs domain's StateAccess. Subscribe
// subscribes LockRelease, which releases a run's locks once its
// run_updated names a terminal state. StackGuard is the guard internal/app
// registers with the stacks domain, whose stack delete enqueues
// StackPurge, one of Jobs; Purge deletes a deleted account's state, which
// internal/app runs on access' account deletion. Held declares the columns that hold
// the access domain's accounts, the stacks domain's stacks and the runs
// domain's runs, for the nightly orphan count; Stream is its migration
// stream, which internal/app registers after runs'.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL, the stacks domain, the
// runs domain, the states store's objects and the job queue through Deps,
// and serves HTTP through the routes it adds.
//
// # Dependencies
//
// It imports runsapi, stacksapi, the domain's backend, core and pg
// packages, chi, platform/outbox, platform/queue, platform/sql/integrity,
// platform/sql/migrate and platform/tx. Only internal/app imports it.
package assembly
