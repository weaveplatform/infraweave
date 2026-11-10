// Package assembly constructs the stacks domain over its Deps. Stacks is
// the stacksapi.Stacks that the domains above it call; SpaceGuard is the
// accessapi.SpaceGuard internal/app registers with the access domain, so
// a space a stack lives in cannot be deleted; GuardStacks registers
// another domain's stacksapi.StackGuard, which a stack's move and delete
// ask; BindConfiguration binds the configuration domain's
// stacksapi.StackConfiguration, the back edge the stack view reads a
// stack's contexts and config elements through; BindSources binds the vcs
// domain's stacksapi.StackSources, which a stack's sync and set of its
// tracked commit resolve refs through; SpaceOf is the
// accessapi.SpaceOf internal/app registers for the entity "stack", so the
// access domain decides a stack's operations in the stack's space, and
// BindingSpaceOf the one it registers for "stack_role_binding", so a
// stack binding's operations are decided in its stack's space too; Deps
// carry the access domain's accessapi.Bindings, through which the domain
// binds its stacks and removes a deleted stack's bindings, and
// accessapi.Grants, which explains the stacks a space's grants reach;
// RegisterEvents registers stacksapi.Changed with the outbox; API is its
// partial of the API's strict server, aliased StacksAPI; Purge deletes a
// deleted account's stacks PurgeBatch at a time, which internal/app runs
// for each accessapi.Deleted, and Held declares the columns that hold the
// access domain's accounts and spaces, and Keys the stacks' key that
// other domains' columns hold, for the nightly orphan count;
// Stream is its migration stream, which internal/app registers after
// access'.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL and the outbox through
// Deps.
//
// # Dependencies
//
// It imports accessapi, stacksapi, the domain's core, http and pg
// packages, internal/kernel, platform/outbox, platform/sql/integrity and
// platform/sql/migrate. Only internal/app
// and cmd/ import it, as the constructors rule enforces.
package assembly
