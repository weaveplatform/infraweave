// Package http is the state domain's HTTP adapter for the API: Server,
// the domain's partial of the API's strict server, which serves the Tofu
// Workspaces operations, stackManagedStateImport,
// stackManagedStateRollback and stackSearchManagedStateVersions.
// searchTofuWorkspaces pages the workspaces of the
// stacks in the spaces the caller may read, and
// searchTofuWorkspacesSuggestions suggests their names and stacks;
// tofuWorkspaceUnlock releases every lock on one workspace and answers
// it, and tofuWorkspaceUnlockAll every lock on a stack's workspaces,
// answering those it unlocked, each recording the caller's address, which
// the access domain's authentication put on the request.
// stackManagedStateImport imports the state file the caller uploaded
// into the stack's default workspace, recording the caller's address,
// and answers true; a stack whose managed state, lock or runs refuse it
// answers 409 invalid-state naming what blocks it, and an upload refused
// 422 pointing at /state. stackManagedStateRollback rolls the default
// workspace back to the version the body names and answers the new
// version; a stack refused as import refuses, or a target that is the
// current version, answers 409 invalid-state, and a version the stack
// lacks 404. stackSearchManagedStateVersions pages the default
// workspace's versions, each answering its ID, size, when it was
// written, its run, its import and its rollback. Each workspace
// answers its ID, its name,
// default for a stack's default workspace, its creation in Unix seconds,
// whether a lock is held and lockedBy, who triggered the run whose lock
// holds it. The route table has decided each operation's action and
// answered 404 for a workspace or stack the caller cannot read; a
// workspace or stack gone before its handler reads it answers 404 too,
// and a search the fields cannot answer 422.
//
// # Input and output
//
// None of its own: it serves the requests the route table hands it.
//
// # Dependencies
//
// It imports accessapi, internal/kernel, kernel/search, stacksapi, the
// domain's core, platform/gen/api, platform/httpkit/apisearch and
// platform/httpkit/problem. The state domain's assembly constructs it.
package http
