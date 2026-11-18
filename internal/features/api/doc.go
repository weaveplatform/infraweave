// Package api is what other domains call of the features domain:
// Entitlements, which creates an organization's entitlement in the
// caller's transaction, reads an
// organization's Entitlement, checks a domain option against it through
// Options, returning a Refusal with the operator or tier reason, and admits a creation against the
// organization's pooled limit and the account's limit, refusing it with a
// LimitError. It also reads, sets and clears an account's limits for the
// Organization tag, refusing a value above the pool or a name of no limit
// with an AccountLimitError, and names the catalogue's limits
// (LimitNames), so the access domain lists them without importing the
// catalogue.
//
// # Input and output
//
// None; it declares types and an interface.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The access and VCS domains
// import it, along their edges of the declared graph; the features domain's
// assembly implements it.
package api
