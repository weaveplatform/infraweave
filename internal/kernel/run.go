package kernel

// RunState is the state of a run, as the API serves it.
//
// ref: report 08 § "Run states"
type RunState string

// The run states. RunStateUnknown is the enum's zero value, which no run
// is ever in. The reference's deprecated PENDING is left out: no run
// reaches it and the API does not serve it.
const (
	RunStateUnknown         RunState = "UNKNOWN"
	RunStateQueued          RunState = "QUEUED"
	RunStateCanceled        RunState = "CANCELED"
	RunStateInitializing    RunState = "INITIALIZING"
	RunStatePlanning        RunState = "PLANNING"
	RunStateFailed          RunState = "FAILED"
	RunStateFinished        RunState = "FINISHED"
	RunStateUnconfirmed     RunState = "UNCONFIRMED"
	RunStateDiscarded       RunState = "DISCARDED"
	RunStateConfirmed       RunState = "CONFIRMED"
	RunStateApplying        RunState = "APPLYING"
	RunStatePerforming      RunState = "PERFORMING"
	RunStateStopped         RunState = "STOPPED"
	RunStateDestroying      RunState = "DESTROYING"
	RunStatePreparing       RunState = "PREPARING"
	RunStatePreparingApply  RunState = "PREPARING_APPLY"
	RunStateSkipped         RunState = "SKIPPED"
	RunStateReplanRequested RunState = "REPLAN_REQUESTED"
	RunStatePreparingReplan RunState = "PREPARING_REPLAN"
	RunStateReady           RunState = "READY"
	RunStatePendingReview   RunState = "PENDING_REVIEW"
)

// runStates are each RunState's flags: whether a run in it has ended, and
// whether it is passive, nothing executing in it. UNKNOWN is neither.
var runStates = map[RunState]struct{ terminal, passive bool }{
	RunStateUnknown:         {},
	RunStateQueued:          {passive: true},
	RunStateCanceled:        {terminal: true, passive: true},
	RunStateInitializing:    {passive: true},
	RunStatePlanning:        {passive: true},
	RunStateFailed:          {terminal: true, passive: true},
	RunStateFinished:        {terminal: true, passive: true},
	RunStateUnconfirmed:     {},
	RunStateDiscarded:       {terminal: true, passive: true},
	RunStateConfirmed:       {},
	RunStateApplying:        {},
	RunStatePerforming:      {},
	RunStateStopped:         {terminal: true, passive: true},
	RunStateDestroying:      {},
	RunStatePreparing:       {passive: true},
	RunStatePreparingApply:  {passive: true},
	RunStateSkipped:         {terminal: true, passive: true},
	RunStateReplanRequested: {},
	RunStatePreparingReplan: {},
	RunStateReady:           {passive: true},
	RunStatePendingReview:   {},
}

// RunStates lists every RunState, UNKNOWN first.
func RunStates() []RunState {
	return []RunState{
		RunStateUnknown, RunStateQueued, RunStateCanceled, RunStateInitializing,
		RunStatePlanning, RunStateFailed, RunStateFinished, RunStateUnconfirmed,
		RunStateDiscarded, RunStateConfirmed, RunStateApplying, RunStatePerforming,
		RunStateStopped, RunStateDestroying, RunStatePreparing, RunStatePreparingApply,
		RunStateSkipped, RunStateReplanRequested, RunStatePreparingReplan, RunStateReady,
		RunStatePendingReview,
	}
}

// Valid reports whether s is a RunState.
func (s RunState) Valid() bool {
	_, ok := runStates[s]
	return ok
}

// Terminal reports whether a run in s has ended, so it leaves s no more.
func (s RunState) Terminal() bool { return runStates[s].terminal }

// Passive reports whether nothing executes in s, as the reference's state
// table flags it.
func (s RunState) Passive() bool { return runStates[s].passive }

// RunType is the kind of a run, which decides the states it passes
// through.
type RunType string

// The run types. PARSE is served and never created; UNKNOWN is the enum's
// zero value.
const (
	RunTypeUnknown  RunType = "UNKNOWN"
	RunTypeProposed RunType = "PROPOSED"
	RunTypeTracked  RunType = "TRACKED"
	RunTypeTask     RunType = "TASK"
	RunTypeTesting  RunType = "TESTING"
	RunTypeDestroy  RunType = "DESTROY"
	RunTypeParse    RunType = "PARSE"
)

// RunTypes lists every RunType, UNKNOWN first.
func RunTypes() []RunType {
	return []RunType{
		RunTypeUnknown, RunTypeProposed, RunTypeTracked, RunTypeTask, RunTypeTesting,
		RunTypeDestroy, RunTypeParse,
	}
}

// Valid reports whether t is a RunType.
func (t RunType) Valid() bool { return t == RunTypeUnknown || t.Blocking() || t.proposed() }

// Blocking reports whether a run of t takes its stack's exclusive lock:
// a tracked run, a task or a destroy.
func (t RunType) Blocking() bool {
	return t == RunTypeTracked || t == RunTypeTask || t == RunTypeDestroy
}

// proposed reports whether t is a run type that never takes the stack's
// lock.
func (t RunType) proposed() bool {
	return t == RunTypeProposed || t == RunTypeTesting || t == RunTypeParse
}

// StackState is a stack's state, the state of its current or last
// tracked run, or NONE before its first.
//
// ref: report 08 § "StackState and RunState"
type StackState string

// StackStateNone is the state of a stack no tracked run has changed.
const StackStateNone StackState = "NONE"

// StackStates lists every StackState: NONE, then each RunState that a
// stack's state mirrors.
func StackStates() []StackState {
	out := []StackState{StackStateNone}
	for _, s := range RunStates() {
		if st, ok := StackStateOf(s); ok {
			out = append(out, st)
		}
	}
	return out
}

// StackStateOf is the stack's state a tracked run in s sets, and false for
// a state that leaves the stack's state as it is: a queued, canceled,
// performing or skipped run, one pending review, or UNKNOWN.
func StackStateOf(s RunState) (StackState, bool) {
	switch s {
	case RunStateUnknown, RunStateQueued, RunStateCanceled, RunStatePerforming,
		RunStateSkipped, RunStatePendingReview:
		return "", false
	}
	if !s.Valid() {
		return "", false
	}
	return StackState(s), true
}

// Valid reports whether s is a StackState.
func (s StackState) Valid() bool {
	if s == StackStateNone {
		return true
	}
	_, ok := StackStateOf(RunState(s))
	return ok
}

// Terminal reports whether no run is ongoing in s: NONE, or the state of
// a run that has ended.
func (s StackState) Terminal() bool { return s == StackStateNone || RunState(s).Terminal() }

// RunPriority is a run's place in its worker pool's queue.
//
// ref: report 08 § "Priority"
type RunPriority string

// The run priorities. A run starts at RunPriorityNormal.
const (
	RunPriorityLow    RunPriority = "low"
	RunPriorityNormal RunPriority = "normal"
	RunPriorityHigh   RunPriority = "high"
)

// RunPriorities lists every RunPriority, lowest first.
func RunPriorities() []RunPriority {
	return []RunPriority{RunPriorityLow, RunPriorityNormal, RunPriorityHigh}
}

// Valid reports whether p is a RunPriority.
func (p RunPriority) Valid() bool {
	return p == RunPriorityLow || p == RunPriorityNormal || p == RunPriorityHigh
}

// RunTier is the tier, 1 first, in which a worker pool takes a waiting run
// of type t at priority p, drift whether it detects drift; within a tier
// the oldest run goes first. A high run comes first whatever its type, and
// drift detection last at each other priority, behind every low run.
func RunTier(p RunPriority, t RunType, drift bool) int {
	low := p == RunPriorityLow
	switch {
	case p == RunPriorityHigh:
		return 1
	case drift && low:
		return 7
	case drift:
		return 6
	case t.Blocking() && low:
		return 4
	case low:
		return 5
	case t.Blocking():
		return 2
	default:
		return 3
	}
}
