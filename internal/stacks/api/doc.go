// Package api is what other domains call of the stacks domain: Stacks,
// which reads an account's stack by its ID or slug as a Stack, several by
// their IDs, its Settings, the stacks that track a repository's branch
// the stacks in some spaces that carry some labels, every stack in some
// spaces and some stacks' labels, locks a stack for a run, holds a stack
// for a domain about to store its ID, sets the Commit a stack tracks and
// its branch's head, writes the runs domain's projection of a stack's run
// state, and detaches the stacks bound to a deleted VCS integration;
// ErrNotFound when the account lacks the stack. A domain whose rows name
// stacks registers a StackGuard, which a stack's move asks what the stack
// would lose, as a StackMove, and its delete asks to delete the domain's
// rows of it, both after the stack's row is locked. StackConfiguration is
// what the stack view reads of the configuration domain, a back edge that
// domain implements, with AttachedContext and ConfigElement its answers,
// and StackStatus what it reads of the runs domain, the run blocking each
// stack. StackSources is what the stack's commit operations resolve refs
// through, a back edge the vcs domain implements, with ErrRefNotFound for
// a ref the repository lacks, and StackIntegrations what a stack's write
// checks the VCS integration it names with, another, with
// ErrNoIntegration for one it may not bind. Changed is the event the
// domain appends to the outbox whenever a stack is created, changed,
// locked, unlocked or deleted, marking a change that
// gives or changes its source, with ChangedSchema its document's schema.
//
// # Input and output
//
// None; it declares types, interfaces and a JSON Schema.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The domains above
// stacks in the declared graph import it; the stacks domain's assembly
// implements Stacks, those domains StackGuard, the configuration domain
// StackConfiguration, the runs domain StackStatus, and the vcs domain
// StackSources and StackIntegrations.
package api
