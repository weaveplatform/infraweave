// Package schedule is the run.schedule job's handler: Scheduler walks a
// stack's QUEUED runs oldest first, after locking the stack's row, and
// moves each to READY through the lifecycle with T2. A blocking run moves
// only once it takes the stack's run lock, so blocking runs pass one at a
// time; a proposed or test run never waits for the lock. A disabled stack
// keeps its runs waiting. Approval and the dependency gates pass until
// approval policies and stack dependencies exist. StackChanged enqueues a
// stack's scheduling when the stack is unlocked or enabled. Store is its
// storage port, and Queue the queue it completes its jobs on.
//
// Claims is the claim of the domain's workersapi.Scheduler, through which
// a worker's poll claims its pool's next run: the first by priority tier, then
// creation, of the runs waiting for a worker, skipping rows other claims
// lock, and the drift detection runs of tiers 6 and 7 while the pool's
// drift runs on workers reach its limit. It moves the run to its
// preparing state through the lifecycle and records the worker. A worker
// that holds a run claims none. ClaimStore is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store and ClaimStore, the
// stacks domain through stacksapi.Stacks, runs through the lifecycle and
// the queue through Queue.
//
// # Dependencies
//
// It imports stacksapi, workersapi, the domain's lifecycle and machine,
// internal/kernel, platform/outbox, platform/queue and platform/tx. The
// runs domain's assembly constructs it.
package schedule
