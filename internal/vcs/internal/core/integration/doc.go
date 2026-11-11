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
// the integration's space. Builtin and SetBuiltin read and set an
// account's check options of a provider whose credentials the operator
// sets, GitHub's, which a stack on it always uses; OpenManifest and
// FromManifest open a GitHub App manifest form and turn the App it
// creates into a GitHub Enterprise integration with the form's ID; and
// Details are an integration's webhook secret and URL and its App as its
// host reports it. Create checks the provider settings every provider
// shares, a Bitbucket Cloud integration's email and an Azure DevOps
// integration's organization URL; Update keeps an
// integration's space and each secret, its credential or webhook secret,
// that it is given none of.
//
// ref: decisions 0007, 0012 and 0013
//
// # Input and output
//
// New takes the Store, the pool its writes begin transactions on, the
// access domain's spaces and authorizer, the stacks domain's api, the
// providers infraweave stores integrations of, each one's Tester, the
// operator's built-in integrations, the GitHub provider's Apps and the
// server's public origin.
//
// # Dependencies
//
// It imports the access and stacks api packages, internal/kernel and its
// search package, platform/tx and the domain's port package. The domain's
// assembly constructs it over the pg adapter.
package integration
