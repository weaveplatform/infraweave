// Package api is what other packages call of the access domain:
// Authenticator, the middleware internal/app puts in front of every API
// route, which puts the principal in each authenticated request's
// context, given each route's Access: Account, Organization or Public.
// LoginPattern is the break-glass login's route, beside the APIs, and
// the OAuth patterns are the authorization server's for MCP clients.
//
// # Input and output
//
// None; it declares interfaces.
//
// # Dependencies
//
// It imports the standard library only. internal/app imports it; the
// access domain's HTTP adapter implements it, and its oauth package
// serves the OAuth patterns.
package api
