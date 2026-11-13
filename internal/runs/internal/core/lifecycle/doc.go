// Package lifecycle is the one place a run is created or changes state.
// Transition moves a run along an edge of the machine in its caller's
// transaction: it locks the run's row, refuses a stale state_version with
// a Conflict and a move no edge allows, or one taken without the edge's
// guards, with a Refused, writes the new state and its transition, and,
// on a terminal state, releases the stack run lock the run holds and the
// source credentials minted for it. A tracked or destroy run in a state
// the stack shows becomes its stack's tracked run, and the stack's state
// and blocked are written into the stacks domain whenever the tracked run
// moves or a lock is released. Each move appends runsapi.Updated to the
// outbox and, when it creates the run, ends it or leaves it waiting for a
// confirmation, enqueues ScheduleKind for its stack; a move out of a
// state a worker holds the run in enqueues CompactKind for that state's
// log. Create creates a run
// in QUEUED once per stack, type and commit among runs that have not
// ended, after locking the stack's row, refusing a disabled stack with
// ErrDisabled and a stack another caller locked with a Locked. Store is
// its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store, the stacks domain
// through stacksapi.Stacks, the outbox through Events, the queue through
// Jobs and the vcs domain through runsapi.RunSources.
//
// # Dependencies
//
// It imports runsapi, stacksapi, the machine, internal/kernel,
// platform/outbox, platform/queue and platform/tx. The runs domain's
// assembly constructs it over the domain's pg adapter.
package lifecycle
