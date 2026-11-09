// Package api is what other domains call of the vendors domain: Catalog,
// which lists each Vendor's Terraform or OpenTofu versions a stack may use,
// newest first, and resolves a version constraint to the newest of them it
// allows, as runs pick a stack's tool and templates check theirs.
//
// # Input and output
//
// None; it declares an interface.
//
// # Dependencies
//
// It imports the standard library alone. Vendor's values are internal/iac's
// kinds, which the vendors domain's tests hold them to. The domains above
// vendors in the declared graph import it; the vendors domain's assembly
// implements it.
package api
