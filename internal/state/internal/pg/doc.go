// Package pg is the state domain's PostgreSQL storage: Stream, its
// migration stream, whose state_workspaces table keys each workspace by
// its account, stack and name, empty for the default workspace, with the
// ID the Tofu Workspaces operations name it by and the version a read
// answers; state_locks holds the shared or exclusive locks runs hold on a
// workspace, with the LockInfo each was taken with and who triggered its
// run, and
// managed_state_versions each version written to it, by its object in the
// states store. No table carries a foreign key to a stack or a run, since
// they belong to the stacks and runs domains. Store makes a workspace
// exist, locks its row while its locks are taken or released, records and
// deletes locks and audit entries, reads a workspace's current version
// and adds one, pointing the workspace at it, and purges a stack's or an
// account's rows a batch at a time, versions first and workspaces last.
// It reads a workspace by its ID with the lock that holds it, locks a
// stack's workspace rows in name order for a release, and searches and
// suggests workspaces over the scope's stacks, which a query binds as
// arrays since stacks belong to the stacks domain. Held names the columns
// that hold the access domain's accounts,
// the stacks domain's stacks and the runs domain's runs. Every method
// joins the transaction its context carries.
//
// # Input and output
//
// It reads and writes PostgreSQL through the querier it is given.
//
// # Dependencies
//
// It imports internal/kernel, kernel/search, stateapi, the domain's core,
// pgx, platform/audit, platform/sql, platform/sql/integrity,
// platform/sql/migrate, platform/sql/search and platform/tx. The state domain's assembly constructs it.
package pg
