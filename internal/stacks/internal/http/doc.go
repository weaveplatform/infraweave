// Package http is the stacks domain's HTTP adapter: Server, the domain's
// partial of the API's strict server, which serves the Stacks operations
// that create, read, list, search, change, patch, reslug, disable,
// enable, lock, unlock, star and delete a stack, sync its tracked commit
// to its branch's head or set it to a commit, and suggest the values a
// stack search filters by. A read, change or patch answers the
// stack's ETag; a change, patch or delete passes its If-Match to the
// service, and a creation passes its stackObjectID, the state file to
// import. A patch merges its body into the stack's settings as a PUT
// would send them and writes the result through the change, through
// platform/httpkit/strict. The route table has already authorized each
// request on the stack's space, or on the space its body names; Server
// answers a stack in its space's slug, with the actions the caller holds
// there, its attached contexts and config elements from the
// stacksapi.StackConfiguration BindConfiguration binds, read once for a
// whole page and empty until one is bound, with a write-only value left
// out, its branch head and tracked commit as the vcs domain resolved
// them, its state and blocked from the runs domain's projection, its
// blocker from the stacksapi.StackStatus SetStackStatus binds, none until
// one is bound, with the blocker's plan delta while it waits in
// UNCONFIRMED, its outputs from the same StackStatus, each value the
// JSON of the tool's output as a string and none for a sensitive output,
// isStateRollback from the stacksapi.StateRollbacks BindStateRollbacks
// binds, read once for a whole page and false until one is bound. Unbound
// domain projections retain their empty values. A Terragrunt
// stack reads back as Terragrunt, its useStateManagement whether
// infraweave keeps its state and its effectiveVersions the Terragrunt and
// tool releases its runs' handover would resolve from the
// vendorsapi.Catalog, each vendor and constraint read once a page, null
// where none is listed that the constraint allows.
// The service's errors answer as problems: a missing action 403 naming it, a
// stack locked by another 409 resource-locked naming the holder, a commit
// operation on a stack whose VCS integration was deleted 409
// invalid-state, a taken
// slug 409 already-exists, a missing stack or space 404, and a stale
// If-Match 412. A stack's run and task lists answer empty pages, refusing
// a cursor of another list as 400 invalid-cursor and a runsInRange span
// that is not Unix seconds at most eight days wide as 422, and a run by
// ID answers 404, until the runs domain serves them.
//
// # Input and output
//
// None of its own: it serves the requests the route table hands it.
//
// # Dependencies
//
// It imports accessapi, stacksapi, vendorsapi, the domain's core package,
// internal/iac, iac/terragrunt, internal/kernel, kernel/search,
// platform/gen/api,
// platform/httpkit/apidelta, platform/httpkit/apisearch,
// platform/httpkit/page,
// platform/httpkit/problem and platform/httpkit/strict. The stacks
// domain's assembly constructs it.
package http
