// Package core is the state domain's core: Service, which opens a stack's
// workspace for a write, holding the stack through Stacks and making the
// workspace exist in the caller's transaction, so the stack's delete waits
// for the write and then deletes it with the stack. It takes and releases
// a workspace's shared and exclusive locks one caller at a time under the
// workspace row's lock, answers a Conflict naming the holder, records an
// audit entry when a caller releases another run's lock, and releases a
// run's locks once it ends. Write stores a state version for a run that
// holds the workspace's exclusive lock under the write's ID, checked
// before the body streams and again as it commits: the body streams once
// through a Content-MD5 check, skipped under fips140=only, and a scan of
// its top-level object, which tells a plain state's serial and lineage
// from a client-encrypted state, into its own sealed object, and one
// transaction makes the object live, records the version and makes it
// current, unless its SHA-256 is the current version's. Read, Latest and
// Outputs read the current version, its metadata and its outputs. As the
// stacks domain's StackGuard, a stack's
// delete enqueues PurgeKind in its transaction; Purge deletes a stack's or
// a deleted account's state PurgeBatch rows at a time, handing each
// version's object to Objects. Store is its storage port.
//
// # Input and output
//
// It reads a version's body as it streams and a stored state's outputs;
// otherwise it reaches storage through Store, the stacks domain through
// Stacks, the states store's objects through Objects and the job queue
// through Jobs.
//
// # Dependencies
//
// It imports internal/kernel, stacksapi, stateapi, platform/objectstore,
// platform/queue, platform/tx, and crypto/fips140 and crypto/md5 for the
// Content-MD5 check. The state domain's assembly constructs
// it over the domain's pg adapter, stacksapi.Stacks, the blob records and
// the queue.
package core
