// Package assembly constructs the runs domain over its Deps, after the
// configuration domain. StackStatus is the stacksapi.StackStatus
// internal/app binds in the stacks domain, the back edge the stack view
// reads each stack's blocker through; StackGuard is the
// stacksapi.StackGuard internal/app registers there, so a stack's delete
// deletes its runs in the same transaction; API is its partial of the
// API's strict server, aliased RunsAPI; Held declares the columns that
// hold the access domain's accounts and the stacks domain's stacks, for
// the nightly orphan count; Stream is its migration stream, which
// internal/app registers after configuration's.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL through Deps.
//
// # Dependencies
//
// It imports stacksapi, the domain's core, http and pg packages,
// platform/sql/integrity, platform/sql/migrate and platform/tx. Only
// internal/app and cmd/ import it, as the constructors rule enforces.
package assembly
