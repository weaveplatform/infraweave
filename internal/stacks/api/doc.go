// Package api is what other domains call of the stacks domain: Stacks,
// which reads an account's stack by its ID as a Stack, ErrNotFound when
// the account lacks it.
//
// # Input and output
//
// None; it declares types and an interface.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The domains above
// stacks in the declared graph import it; the stacks domain's assembly
// implements it.
package api
