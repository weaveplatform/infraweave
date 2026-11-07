// Package api is what other domains call of the features domain:
// Entitlements, which creates and deletes an organization's entitlement
// and an account's limits in the caller's transaction, reads an
// organization's Entitlement, and admits a creation against the
// organization's pooled limit and the account's limit, refusing it with a
// LimitError.
//
// # Input and output
//
// None; it declares types and an interface.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The access domain
// imports it, along its edge of the declared graph; the features domain's
// assembly implements it.
package api
