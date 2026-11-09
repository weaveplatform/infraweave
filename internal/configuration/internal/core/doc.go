// Package core is the configuration domain's model and rules: a context in
// a space, its config elements, environment variables and mounted files
// whose values the store seals, and its attachments to stacks. Service
// creates, changes and deletes contexts, sets and deletes their elements,
// and attaches them to the stacks within reach: a stack in a space reaches
// the contexts of that space and of each ancestor it climbs to while every
// space on the way inherits. SpaceGuard and StackGuard refuse a space
// delete or move, and a stack move, that would leave an attachment out of
// reach, and delete a deleted stack's attachments and elements; the
// Service is also the stack view's reader of a stack's contexts and
// elements.
//
// # Input and output
//
// None: the store, the spaces, the stacks, the authorizer and the outbox
// are interfaces its callers pass.
//
// # Dependencies
//
// It imports the access, stacks and configuration api packages,
// internal/kernel and its search package, platform/outbox and
// platform/tx. The configuration domain's
// assembly constructs it over the pg store.
package core
