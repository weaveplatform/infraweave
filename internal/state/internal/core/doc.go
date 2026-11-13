// Package core is the state domain's core: Service, which opens a stack's
// workspace for a write, holding the stack through Stacks and making the
// workspace exist in the caller's transaction, so the stack's delete waits
// for the write and then deletes it with the stack. It takes and releases
// a workspace's shared and exclusive locks one caller at a time under the
// workspace row's lock, answers a Conflict naming the holder, records an
// audit entry when a caller releases another run's lock, and releases a
// run's locks once it ends. As the stacks domain's StackGuard, a stack's
// delete enqueues PurgeKind in its transaction; Purge deletes a stack's or
// a deleted account's state PurgeBatch rows at a time, handing each
// version's object to Objects. Store is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store, the stacks domain
// through Stacks, the states store's objects through Objects and the job
// queue through Jobs.
//
// # Dependencies
//
// It imports internal/kernel, stacksapi, platform/objectstore,
// platform/queue and platform/tx. The state domain's assembly constructs
// it over the domain's pg adapter, stacksapi.Stacks, the blob records and
// the queue.
package core
