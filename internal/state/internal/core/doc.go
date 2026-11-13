// Package core is the state domain's core: Service, which opens a stack's
// workspace for a write, holding the stack through Stacks and making the
// workspace exist in the caller's transaction, so the stack's delete waits
// for the write and then deletes it with the stack. It takes and releases
// a workspace's shared and exclusive locks one caller at a time under the
// workspace row's lock, answers a Conflict naming the holder, records an
// audit entry when a caller releases another run's lock, and releases a
// run's locks once it ends. Store is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store and the stacks domain
// through Stacks.
//
// # Dependencies
//
// It imports internal/kernel and platform/tx. The state domain's assembly
// constructs it over the domain's pg adapter and stacksapi.Stacks.
package core
