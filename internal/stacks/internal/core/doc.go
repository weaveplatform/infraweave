// Package core is the stacks domain's logic: Service creates, reads,
// searches, changes, moves, reslugs, disables, enables, locks, unlocks,
// stars and deletes an account's stacks for the HTTP adapter, suggests
// the values of their searchable Fields, and reads them for other domains
// through stacksapi.Stacks. A search or suggestion sees only the stacks in
// the spaces the caller may read, with the account's space tree read
// through accessapi.Spaces, and the caller's own stars. Each write checks the
// caller's action in the stack's space through the access domain, holds
// that space, and moving a stack needs STACK_CREATE in the space it moves
// to, and asks each registered stacksapi.StackGuard what the stack would
// lose, refusing the move as a *kernel.Invalid naming every entity; a
// delete lets each guard delete its rows of the stack first, in its
// transaction. Both ask after the stack's row is locked FOR UPDATE, which
// waits for every Hold. A write bumps the stack's version and appends
// stacksapi.Changed in
// the same transaction; Update and Delete take the kernel.Expect of the
// request's If-Match and check it against the version they read under
// the row's lock, so a stale one changes nothing. Input's settings are
// checked, with each refusal's pointer, before anything is read; labels
// follow kernel.CheckLabels, and Terraform and OpenTofu settings are
// checked by internal/iac. Service is also the domain's
// accessapi.SpaceGuard, refusing deleting a space a stack lives in, and
// the accessapi.SpaceOf of the entity "stack". It refuses no space move,
// since a stack keeps its space through one. Find lists the stacks in
// some spaces that carry some labels, and none without a space or a
// label; InSpaces lists every stack in some spaces and Labels some
// stacks' labels; BySlug and ByIDs read stacks for the domains that
// attach to them. SyncCommit sets a stack's branch head and tracked
// commit to its branch's head, and SetCurrentCommit its tracked commit to
// a commit of its repository, resolving each through the
// stacksapi.StackSources BindSources binds, outside any transaction; a ref
// the repository lacks is a *kernel.Invalid, at /sha for a commit, and
// no bound source kernel.ErrUnavailable. SetTracked writes them for the
// vcs domain; neither changes the stack's version. A write's
// stacksapi.Changed marks whether it gave or changed the stack's source:
// its provider, VCS integration, namespace, repository, URL or branch. A
// write naming a VCS integration binds it only when the
// stacksapi.StackIntegrations BindIntegrations binds allows it, refused
// at vcsIntegrationId otherwise and before one is bound. DetachVCS
// detaches the stacks bound to a deleted integration, without changing
// their versions; a detached stack's commit operations are a *Detached
// until a write gives it a source again. Bind, BindAll, Rebind and Unbind write a stack's role
// bindings through accessapi.Bindings: each needs STACK_MANAGE on the
// stack's space and SPACE_ADMIN on the space bound in, the first
// decided by the route for an operation on one binding, and a binding on
// the top space needs the stack to live there; each holds the stack, so
// a delete, which removes the stack's bindings in its transaction, waits
// for it. A binding stays where it is when its stack moves.
// BindingSpaceOf is the accessapi.SpaceOf of the entity
// "stack_role_binding", its stack's space. Holding and SuggestHolding
// search the stacks a space's access view lists: those that live in the
// space, or, for a space admin, every stack accessapi.Grants finds a grant
// of reaching it, its implicit read of its own space among them.
// SearchUsers and SuggestUsers search the stacks the caller may read that
// use a VCS integration: those bound to it and, for a provider's default,
// those on its provider that name none.
//
// # Input and output
//
// None of its own: it reaches PostgreSQL through its Store and the outbox
// through its Events.
//
// # Dependencies
//
// It imports accessapi, stacksapi, internal/iac, internal/kernel,
// kernel/search, platform/outbox and platform/tx, and the standard
// library's slices, strings and sync. The stacks domain's
// assembly constructs it over the pg package's Store.
package core
