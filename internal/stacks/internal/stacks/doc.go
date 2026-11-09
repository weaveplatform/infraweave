// Package stacks is the stacks domain's logic: Service creates, reads,
// lists, changes, moves, reslugs, disables, enables, locks, unlocks,
// stars and deletes an account's stacks for the HTTP adapter, and reads
// them for other domains through stacksapi.Stacks. Each write checks the
// caller's action in the stack's space through the access domain, holds
// that space, and moving a stack needs STACK_CREATE in the space it moves
// to. A write bumps the stack's version and appends stacksapi.Changed in
// the same transaction. Input's settings are checked, with each refusal's
// pointer, before anything is read; Terraform and OpenTofu settings are
// checked by internal/iac. Service is also the domain's
// accessapi.SpaceGuard, refusing deleting a space a stack lives in, and
// the accessapi.SpaceOf of the entity "stack". It refuses no move yet,
// since nothing a stack reaches through its space is attached to it.
//
// # Input and output
//
// None of its own: it reaches PostgreSQL through its Store and the outbox
// through its Events.
//
// # Dependencies
//
// It imports accessapi, stacksapi, internal/iac, internal/kernel,
// kernel/search, platform/outbox and platform/tx. The stacks domain's
// assembly constructs it over the pg package's Store.
package stacks
