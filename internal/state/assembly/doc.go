// Package assembly constructs the state domain over its Deps, after the
// runs domain: the core that opens a stack's workspace under the stack's
// hold, over the domain's pg adapter. Held declares the columns that hold
// the access domain's accounts, the stacks domain's stacks and the runs
// domain's runs, for the nightly orphan count; Stream is its migration
// stream, which internal/app registers after runs'.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL and the stacks domain
// through Deps.
//
// # Dependencies
//
// It imports stacksapi, the domain's core and pg packages,
// platform/sql/integrity, platform/sql/migrate and platform/tx. Only
// internal/app imports it.
package assembly
