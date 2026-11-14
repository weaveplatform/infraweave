// Package timers is run.timers, the periodic task that ends the runs
// nobody moves: a proposed or test run not picked up
// within run.timers.pickup_proposed, and a tracked run, task or destroy
// run left in READY for run.timers.pickup_blocking, are CANCELED; a
// tracked run UNCONFIRMED for run.timers.unconfirmed unchanged is
// DISCARDED, and one CONFIRMED or REPLAN_REQUESTED for
// run.timers.confirmed unchanged FAILED; and any run that waits in one of
// those states, or for approval in PENDING_REVIEW, run.timers.max_age
// after its creation ends by its state, that rule going first, since it
// never notifies. A run on a worker is liveness's, offline worker or not,
// so no rule reads one.
//
// Service.Pass reads the runs a rule times out through Store, oldest
// state change first, Batch to a transaction and at most Batches
// transactions a pass, skipping rows another transaction locks, and moves
// each through the lifecycle by the timers' edge out of its state, which
// the machine names, with a note naming the rule and the rule on its
// run_updated event. Settings are run.timers, and Kind and Every the
// periodic task's.
//
// ref: decision 0008 § "Timeouts and timers"
// ref: report 08 § "Timers"
//
// # Input and output
//
// New takes the database, the Store, the lifecycle, run.timers and a
// clock. Pass answers the runs it ended by rule; it logs nothing, and the
// domain's assembly logs a pass that ended any.
//
// # Dependencies
//
// It imports runsapi, internal/kernel, platform/queue, platform/tx and the
// domain's lifecycle and machine. The domain's assembly constructs it over
// the pg adapter and registers its periodic task.
package timers
