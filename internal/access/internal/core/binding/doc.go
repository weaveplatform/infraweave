// Package binding holds role bindings: Binding is one grant of a role to
// an actor (a user, group, API key, stack or Flows project) in a space and
// the spaces below it, made as a role binding or from a legacy access
// rule, its Origin. Service creates, moves and deletes the bindings of
// users, groups and API keys. Prepare checks a request's elements, each
// naming its actor, role and space by ID and slugs: one whose actor, role
// or space the account lacks, or whose space the caller cannot read, is
// refused as not-found, and one whose space the caller does not
// administer under the rule needing SPACE_ADMIN, each at its pointer and
// field in one *kernel.Invalid. Create records a request's bindings all
// or none, a binding the account holds a *Duplicate naming its index;
// Bind records ones Prepare made for an actor created beside them, in
// the caller's transaction; ReplaceRules swaps an actor's access rules,
// and Releasable refuses dropping one in a space the caller does not
// administer. Update moves a binding to another role or space, never
// another actor, and Update and Delete check the If-Match against the
// version they lock. Each write drops the account's authorization
// snapshot on this replica.
//
// ref: decision 0007 § "Bindings"
//
// # Input and output
//
// None: the access domain's pg adapter stores bindings, its http adapter
// serves the role binding operations and the legacy inputs through
// Service, and its assembly serves accessapi.Bindings from it.
//
// # Dependencies
//
// It imports accessapi for the Authorizer and its actions, core/port for
// ErrNotFound, internal/kernel and the standard library. The access
// domain's role package, adapters and assembly import it.
package binding
