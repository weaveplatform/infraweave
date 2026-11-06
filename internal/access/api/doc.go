// Package api is what other packages call of the access domain:
// Authenticator, the middleware internal/app puts in front of every API
// route, which puts the principal in each authenticated request's
// context.
//
// # Input and output
//
// None; it declares interfaces.
//
// # Dependencies
//
// It imports the standard library only. internal/app imports it; the
// access domain's HTTP adapter implements it.
package api
