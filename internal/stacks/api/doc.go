// Package api is what other domains call of the stacks domain: Stacks,
// which reads an account's stack by its ID or slug as a Stack, several by
// their IDs, its Settings, the stacks that track a repository's branch
// and the stacks in some spaces that carry some labels, locks a stack for
// a run, and holds a stack for a domain about to store its ID;
// ErrNotFound when the account lacks the stack. A domain whose rows name stacks registers a StackGuard, which a
// stack's move asks what the stack would lose, as a StackMove, and its
// delete asks to delete the domain's rows of it, both after the stack's
// row is locked. StackConfiguration is what the stack view reads of the
// configuration domain, a back edge that domain implements, with
// AttachedContext and ConfigElement its answers. Changed is the event the
// domain appends to the outbox whenever a stack is created, changed,
// locked, unlocked or deleted, with ChangedSchema its document's schema.
//
// # Input and output
//
// None; it declares types, interfaces and a JSON Schema.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The domains above
// stacks in the declared graph import it; the stacks domain's assembly
// implements Stacks, those domains StackGuard, and the configuration
// domain StackConfiguration.
package api
