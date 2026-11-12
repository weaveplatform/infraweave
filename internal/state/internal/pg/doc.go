// Package pg is the state domain's PostgreSQL storage: Stream, its
// migration stream, whose state_workspaces table keys each workspace by
// its account, stack and name, empty for the default workspace, with the
// version a read answers; state_locks holds the shared or exclusive locks
// runs hold on a workspace, with the LockInfo each was taken with, and
// managed_state_versions each version written to it, by its object in the
// states store. No table carries a foreign key to a stack or a run, since
// they belong to the stacks and runs domains. Store makes a workspace
// exist. Held names the columns that hold the access domain's accounts,
// the stacks domain's stacks and the runs domain's runs. Every method
// joins the transaction its context carries.
//
// # Input and output
//
// It reads and writes PostgreSQL through the querier it is given.
//
// # Dependencies
//
// It imports internal/kernel, platform/sql, platform/sql/integrity,
// platform/sql/migrate and platform/tx. The state domain's assembly
// constructs it.
package pg
