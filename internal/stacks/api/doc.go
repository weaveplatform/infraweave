// Package api is what other domains call of the stacks domain: Stacks,
// which reads an account's stack by its ID as a Stack, its Settings, the
// stacks that track a repository's branch, and locks a stack for a run;
// ErrNotFound when the account lacks the stack. Changed is the event the
// domain appends to the outbox whenever a stack is created, changed,
// locked, unlocked or deleted, with ChangedSchema its document's schema.
//
// # Input and output
//
// None; it declares types, an interface and a JSON Schema.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The domains above
// stacks in the declared graph import it; the stacks domain's assembly
// implements it.
package api
