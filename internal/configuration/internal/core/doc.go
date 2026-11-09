// Package core is the configuration domain's model and rules: a context in
// a space, its config elements, environment variables and mounted files
// whose values the store seals, and its attachments to stacks. Service
// creates, changes and deletes contexts, sets and deletes their elements
// and a stack's own, and attaches contexts to the stacks within reach: a
// stack in a space reaches the contexts of that space and of each
// ancestor it climbs to while every space on the way inherits. A context
// labelled autoattach:<label> attaches itself to every stack in reach
// that carries the label, and autoattach:* to every stack in reach: a
// context's write brings its automatic attachments in line in its
// transaction, and StackChanged a stack's, after each stack change, both
// under one lock per account so neither misses the other's commit. An
// automatic attachment is not detached by hand. Resolve hands a run's spec
// a stack's environment, opening every value: its own elements outrank an
// attached context's, a context attached by hand one attached
// automatically, a lower priority a higher one, and among those attached
// automatically the first by name; each phase's hooks run outermost
// first before it and last after it. Surface declares the environment
// expression surface, env and stack, and a value's ${{ … }} templates
// are checked against it when saved through the Checker internal/app
// binds: on a stack a read of a variable it does not resolve to is
// refused, and a value that reads a write-only one is write-only.
// SpaceGuard and StackGuard
// refuse a space delete or move, and a stack move, that would leave an
// attachment made by hand out of reach, and delete a deleted stack's
// attachments and elements; the Service is also the stack view's reader
// of a stack's contexts and elements.
//
// # Input and output
//
// None: the store, the spaces, the stacks, the authorizer and the outbox
// are interfaces its callers pass.
//
// # Dependencies
//
// It imports the access, stacks and configuration api packages,
// internal/kernel and its search package, platform/expr, platform/outbox
// and platform/tx. The configuration domain's
// assembly constructs it over the pg store.
package core
