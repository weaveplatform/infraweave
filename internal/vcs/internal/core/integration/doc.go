// Package integration keeps an account's VCS integrations: Service
// creates, updates and deletes them, at most one default per provider, in
// the account's top space, with their credential and webhook secret
// sealed; reads, searches and lists those a stack in a space may bind,
// the spaces its caller may read keeping the rest out; runs a provider's
// Tester; and answers which integration a stack uses, its own or its
// provider's default, with its secrets opened for the provider's client
// alone. A delete detaches the stacks bound to the integration in its
// transaction. Bindable is the stacks domain's check of the integration a
// stack's write names, one of its provider in its space or one it
// inherits from, and SpaceGuard refuses deleting a space an integration
// lives in. A provider details only reach a caller with SPACE_ADMIN in
// the integration's space.
//
// ref: decisions 0007, 0012 and 0013
//
// # Input and output
//
// New takes the Store, the pool its writes begin transactions on, the
// access domain's spaces and authorizer, the stacks domain's api, the
// providers infraweave stores integrations of and each one's Tester.
//
// # Dependencies
//
// It imports the access and stacks api packages, internal/kernel and its
// search package, platform/tx and the domain's port package. The domain's
// assembly constructs it over the pg adapter.
package integration
