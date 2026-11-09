// Package api is what other packages call of the access domain:
// Authenticator, the middleware internal/app puts in front of every API
// route, which puts the principal in each authenticated request's
// context, given each route's Access: Account, Organization or Public.
// LoginPattern is the break-glass login's route, beside the APIs, and
// the OAuth patterns are the authorization server's for MCP clients.
// Authorizer decides the authorization checks for every domain: whether a
// principal may perform an action in a space, the spaces in which it may,
// whether it holds an action in any, and the actions it holds in one.
// Requirer is the middleware internal/app puts after authentication,
// deciding each operation's Required: its action on its Subject, which
// ParseSubject reads from x-infraweave-subject, naming the space through
// a path parameter, a body field or a query parameter, or none; a domain
// resolves its
// entities' spaces for it with a SpaceOf, ErrNoSubject for an entity the
// account lacks. Spaces is what a domain that stores a space's ID calls to
// find a space by its slug, read the slugs of an account's spaces and
// hold a space in its transaction, ErrNoSpace for one the account lacks,
// and SpaceGuard is what such a domain registers, so the space tree
// refuses a delete or a move that would strand its rows. Action is the
// action catalogue roles are built from: the reference's Action enum, which
// specref generates into actions.gen.go from the introspection snapshot
// and actions.yaml's rows (subject, fallback, disabled, deprecated), then
// infraweave's own account actions on API keys, sessions, managed users
// and groups (Infraweave, and Anywhere for the reads any space's admin
// holds), and the pseudo-actions ACCOUNT_ADMIN, ORGANIZATION_ADMIN and
// AUTHENTICATED; Actions and ActionOf read it. Deleted is the event an
// account's deletion appends, one per account of a deleted organization,
// with DeletedSchema its document's schema; the domains that keep an
// account's rows consume it and delete them.
//
// ref: decision 0007
//
// # Input and output
//
// None; it declares interfaces and types.
//
// # Dependencies
//
// It imports internal/kernel and the standard library. internal/app
// imports it; the access domain's HTTP adapter implements it, its authz
// package implements Authorizer, its oauth
// package serves the OAuth patterns, its space package implements Spaces
// and consults each SpaceGuard, and its role package builds the system roles from the
// catalogue.
package api
