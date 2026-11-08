// Package api is what other packages call of the access domain:
// Authenticator, the middleware internal/app puts in front of every API
// route, which puts the principal in each authenticated request's
// context, given each route's Access: Account, Organization or Public.
// LoginPattern is the break-glass login's route, beside the APIs, and
// the OAuth patterns are the authorization server's for MCP clients.
// SpaceGuard is what a domain whose rows name spaces registers, so the
// space tree refuses a delete or a move that would strand them. Action is
// decision 0007's action catalogue: the reference's Action enum, which
// specref generates into actions.gen.go from the introspection snapshot
// and actions.yaml's rows (subject, fallback, disabled, deprecated), and
// the pseudo-actions ACCOUNT_ADMIN, ORGANIZATION_ADMIN and AUTHENTICATED;
// Actions and ActionOf read it.
//
// # Input and output
//
// None; it declares interfaces and types.
//
// # Dependencies
//
// It imports internal/kernel and the standard library. internal/app
// imports it; the access domain's HTTP adapter implements it, its oauth
// package serves the OAuth patterns, its space package consults each
// SpaceGuard, and its role package builds the system roles from the
// catalogue.
package api
