// Package assembly constructs the state domain over its Deps, after the
// runs domain: the core that opens a stack's workspace under the stack's
// hold, takes and releases its locks and writes and reads its versions,
// over the domain's pg adapter and Objects, and the backend Routes adds
// to the API listener under /state/v1, which decides a run's request on
// its own stack through the runs domain's StateAccess and any other read
// through the access domain's StateReader, and bounds a version by
// Settings.MaxSize, state.max_size. Cron is its periodic task, Retention,
// state.retention, which deletes daily each version a newer one replaced
// more than Settings.NoncurrentRetention, state.noncurrent_retention,
// ago, keeping every version under 0, a workspace's current version and
// those of the runs the runs domain's RunEnds answers have not ended, and
// logs how many it deleted. API is the domain's partial of the
// API's strict server, StateAPI, which embeds two: stateUploadUrl and
// stateDownloadUrl, which sign URLs under the installation's keyring,
// absolute under server.public_url and lasting
// objectstore.signed_url_ttl, and the Tofu Workspaces operations,
// stackManagedStateImport, stackManagedStateRollback and
// stackSearchManagedStateVersions, served through the domain's http
// adapter, which reads the spaces a caller may read through Authz and the
// runs an import or a rollback waits on through the runs domain's
// StackRuns. Routes adds the routes that verify the signed URLs
// beside the backend's, and a nil domain's routes answer 501. SpaceOf
// resolves the Entity subject, a Tofu workspace, to its stack's space.
// Versions is what other domains read of a stack's managed state. Subscribe
// subscribes LockRelease, which releases a run's locks once its
// run_updated names a terminal state. StackGuard is the guard internal/app
// registers with the stacks domain, whose stack delete enqueues
// StackPurge, one of Jobs; ManagedState is the stacksapi.ManagedState it
// binds there, which opens the default workspace of a new stack whose
// state infraweave keeps in the creation's transaction and imports the
// state file the creation names; StateRollbacks is the
// stacksapi.StateRollbacks it binds there too, which tells the stack view
// the stacks whose current version is a rollback; Purge deletes a deleted account's state, which
// internal/app runs on access' account deletion. Held declares the columns that hold
// the access domain's accounts, the stacks domain's stacks and the runs
// domain's runs, for the nightly orphan count; Stream is its migration
// stream, which internal/app registers after runs'.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL, the stacks domain, the
// access domain's decisions, the runs domain, the states store's objects
// and the job queue through Deps, serves HTTP through the routes it adds
// and its partial of the API, and logs each retention pass that deleted
// versions to the logger Cron is given.
//
// # Dependencies
//
// It imports accessapi, runsapi, stacksapi, stateapi, the domain's
// backend, core, http, pg, signed and urls packages, chi, platform/cron,
// platform/outbox, platform/queue, platform/sql/integrity,
// platform/sql/migrate and platform/tx. Only internal/app imports it.
package assembly
