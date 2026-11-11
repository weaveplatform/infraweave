package machine

import (
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Driver is what takes a transition: who or what moved the run.
//
// ref: decision 0008 § "Who drives each transition"
type Driver string

// The drivers.
const (
	// DriverAPI is a caller's action through the API: discard, cancel,
	// stop once the worker confirms, confirm and targeted replan.
	DriverAPI Driver = "api"
	// DriverCreation is a route that creates a run, in QUEUED.
	DriverCreation Driver = "creation"
	// DriverScheduler is the run.schedule job.
	DriverScheduler Driver = "scheduler"
	// DriverApproval is an approval policy's decision.
	DriverApproval Driver = "approval"
	// DriverWorkerClaim is a worker's poll claiming the run.
	DriverWorkerClaim Driver = "worker-claim"
	// DriverWorkerReport is a runner's or launcher's report.
	DriverWorkerReport Driver = "worker-report"
	// DriverDecision is the server's choice after a plan, from its plan
	// policies.
	DriverDecision Driver = "decision"
	// DriverTimers is the run.timers periodic task.
	DriverTimers Driver = "timers"
	// DriverLiveness is the worker.reap periodic task, which fails a run
	// its worker lost.
	DriverLiveness Driver = "liveness"
)

// Drivers lists every Driver.
func Drivers() []Driver {
	return []Driver{
		DriverAPI, DriverCreation, DriverScheduler, DriverApproval, DriverWorkerClaim,
		DriverWorkerReport, DriverDecision, DriverTimers, DriverLiveness,
	}
}

// Guard names a condition lifecycle checks before it takes an edge.
//
// ref: decision 0008 § "From QUEUED to READY"
type Guard string

// The guards.
const (
	// GuardStackLockFree holds when no run holds the stack's lock, and
	// always for a run of a type that takes none.
	GuardStackLockFree Guard = "stack-lock-free"
	// GuardApproved holds when the run's approval policies approve.
	GuardApproved Guard = "approved"
	// GuardUpstreamFinished holds when every upstream run the run depends
	// on has finished.
	GuardUpstreamFinished Guard = "upstream-finished"
	// GuardExternalSettled holds when every external dependency of the
	// run is FINISHED or SKIPPED.
	GuardExternalSettled Guard = "external-dependencies-settled"
	// GuardStackEnabled holds when the run's stack is not disabled.
	GuardStackEnabled Guard = "stack-enabled"
)

// Created is the state a run being created leaves: none.
const Created kernel.RunState = ""

// Edge is one transition: its register ID, the state it leaves and the
// one it enters, the run types that take it, the drivers that may and
// the guards that must hold.
type Edge struct {
	ID      string
	From    kernel.RunState
	To      kernel.RunState
	Types   []kernel.RunType
	Drivers []Driver
	Guards  []Guard
}

// Edges lists every edge, in the register's order.
func Edges() []Edge {
	out := make([]Edge, len(edges))
	for i, e := range edges {
		out[i] = e.clone()
	}
	return out
}

// Allowed returns the edge a run of type t takes from from to to when d
// drives it, and false when no edge allows it.
func Allowed(t kernel.RunType, from, to kernel.RunState, d Driver) (Edge, bool) {
	for _, e := range edges {
		if e.From == from && e.To == to && slices.Contains(e.Types, t) &&
			slices.Contains(e.Drivers, d) {
			return e.clone(), true
		}
	}
	return Edge{}, false
}

// Next lists the edges a run of type t may take out of from, in the
// register's order.
func Next(t kernel.RunType, from kernel.RunState) []Edge {
	var out []Edge
	for _, e := range edges {
		if e.From == from && slices.Contains(e.Types, t) {
			out = append(out, e.clone())
		}
	}
	return out
}

func (e Edge) clone() Edge {
	e.Types = slices.Clone(e.Types)
	e.Drivers = slices.Clone(e.Drivers)
	e.Guards = slices.Clone(e.Guards)
	return e
}

// all is every run type that is created: the register's "all". PARSE is
// served and never created, and UNKNOWN is no type.
var all = []kernel.RunType{
	kernel.RunTypeProposed, kernel.RunTypeTracked, kernel.RunTypeTask,
	kernel.RunTypeTesting, kernel.RunTypeDestroy,
}

const (
	proposed = kernel.RunTypeProposed
	tracked  = kernel.RunTypeTracked
	task     = kernel.RunTypeTask
	test     = kernel.RunTypeTesting
	destroy  = kernel.RunTypeDestroy

	queued          = kernel.RunStateQueued
	ready           = kernel.RunStateReady
	preparing       = kernel.RunStatePreparing
	initializing    = kernel.RunStateInitializing
	planning        = kernel.RunStatePlanning
	pendingReview   = kernel.RunStatePendingReview
	unconfirmed     = kernel.RunStateUnconfirmed
	confirmed       = kernel.RunStateConfirmed
	replanRequested = kernel.RunStateReplanRequested
	preparingReplan = kernel.RunStatePreparingReplan
	preparingApply  = kernel.RunStatePreparingApply
	applying        = kernel.RunStateApplying
	destroying      = kernel.RunStateDestroying
	performing      = kernel.RunStatePerforming
	finished        = kernel.RunStateFinished
	failed          = kernel.RunStateFailed
	canceled        = kernel.RunStateCanceled
	discarded       = kernel.RunStateDiscarded
	stopped         = kernel.RunStateStopped
	skipped         = kernel.RunStateSkipped
)

func types(t ...kernel.RunType) []kernel.RunType { return t }

func by(d ...Driver) []Driver { return d }

// edges is the register's T1 to T44, each with its drivers: liveness
// fails a run out of each state a worker holds it in, and a stop is an
// API action the worker then reports.
//
// ref: report 08 § "Transition table"
// ref: decision 0008 § "Who drives each transition"
var edges = []Edge{
	{ID: "T1", From: Created, To: queued, Types: all, Drivers: by(DriverCreation)},
	{
		ID: "T2", From: queued, To: ready, Types: all, Drivers: by(DriverScheduler),
		Guards: []Guard{
			GuardStackLockFree, GuardApproved, GuardUpstreamFinished, GuardExternalSettled,
			GuardStackEnabled,
		},
	},
	{ID: "T3", From: queued, To: discarded, Types: all, Drivers: by(DriverAPI)},
	{
		ID: "T4", From: queued, To: canceled, Types: all,
		Drivers: by(DriverAPI, DriverScheduler, DriverTimers),
	},
	{
		ID: "T5", From: queued, To: failed, Types: all,
		Drivers: by(DriverScheduler, DriverApproval),
	},
	{ID: "T6", From: queued, To: skipped, Types: types(tracked), Drivers: by(DriverScheduler)},
	{ID: "T7", From: ready, To: preparing, Types: all, Drivers: by(DriverWorkerClaim)},
	{ID: "T8", From: ready, To: discarded, Types: all, Drivers: by(DriverAPI)},
	{ID: "T9", From: ready, To: canceled, Types: all, Drivers: by(DriverAPI, DriverTimers)},
	{ID: "T10", From: preparing, To: initializing, Types: all, Drivers: by(DriverWorkerReport)},
	{
		ID: "T11", From: preparing, To: failed, Types: all,
		Drivers: by(DriverWorkerReport, DriverLiveness),
	},
	{
		ID: "T12", From: initializing, To: planning, Types: types(proposed, tracked, test),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T13", From: initializing, To: performing, Types: types(task),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T14", From: initializing, To: destroying, Types: types(destroy),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T15", From: initializing, To: failed, Types: all,
		Drivers: by(DriverWorkerReport, DriverLiveness),
	},
	{
		ID: "T16", From: initializing, To: stopped, Types: all,
		Drivers: by(DriverAPI, DriverWorkerReport),
	},
	{
		ID: "T17", From: planning, To: finished, Types: types(proposed, tracked),
		Drivers: by(DriverDecision),
	},
	{
		ID: "T18", From: planning, To: failed, Types: types(proposed, tracked, test),
		Drivers: by(DriverWorkerReport, DriverDecision, DriverLiveness),
	},
	{
		ID: "T19", From: planning, To: stopped, Types: types(proposed, tracked),
		Drivers: by(DriverAPI, DriverWorkerReport),
	},
	{
		ID:      "T20",
		From:    planning,
		To:      unconfirmed,
		Types:   types(tracked),
		Drivers: by(DriverDecision),
	},
	{
		ID: "T21", From: planning, To: applying, Types: types(tracked, test),
		Drivers: by(DriverDecision),
	},
	{
		ID: "T22", From: planning, To: pendingReview, Types: types(proposed),
		Drivers: by(DriverDecision),
	},
	{
		ID: "T23", From: pendingReview, To: finished, Types: types(proposed),
		Drivers: by(DriverApproval),
	},
	{
		ID: "T24", From: pendingReview, To: failed, Types: types(proposed),
		Drivers: by(DriverApproval),
	},
	{
		ID: "T25", From: pendingReview, To: canceled, Types: types(proposed),
		Drivers: by(DriverTimers),
	},
	{
		ID: "T26", From: unconfirmed, To: confirmed, Types: types(tracked), Drivers: by(DriverAPI),
		Guards: []Guard{GuardApproved},
	},
	{
		ID: "T27", From: unconfirmed, To: discarded, Types: types(tracked),
		Drivers: by(DriverAPI, DriverTimers),
	},
	{
		ID: "T28", From: unconfirmed, To: replanRequested, Types: types(tracked),
		Drivers: by(DriverAPI),
	},
	{
		ID: "T29", From: unconfirmed, To: failed, Types: types(tracked),
		Drivers: by(DriverApproval),
	},
	{
		ID: "T30", From: replanRequested, To: preparingReplan, Types: types(tracked),
		Drivers: by(DriverWorkerClaim),
	},
	{
		ID: "T31", From: replanRequested, To: failed, Types: types(tracked),
		Drivers: by(DriverTimers),
	},
	{
		ID: "T32", From: preparingReplan, To: planning, Types: types(tracked),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T33", From: confirmed, To: preparingApply, Types: types(tracked),
		Drivers: by(DriverWorkerClaim),
	},
	{ID: "T34", From: confirmed, To: failed, Types: types(tracked), Drivers: by(DriverTimers)},
	{
		ID: "T35", From: preparingApply, To: applying, Types: types(tracked),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T36", From: preparingApply, To: failed, Types: types(tracked),
		Drivers: by(DriverWorkerReport, DriverLiveness),
	},
	{
		ID: "T37", From: applying, To: finished, Types: types(tracked),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T38", From: applying, To: failed, Types: types(tracked),
		Drivers: by(DriverWorkerReport, DriverLiveness),
	},
	{
		ID: "T39", From: applying, To: destroying, Types: types(test),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T40", From: destroying, To: finished, Types: types(test, destroy),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T41", From: destroying, To: failed, Types: types(test, destroy),
		Drivers: by(DriverWorkerReport, DriverLiveness),
	},
	{
		ID: "T42", From: performing, To: finished, Types: types(task),
		Drivers: by(DriverWorkerReport),
	},
	{
		ID: "T43", From: performing, To: failed, Types: types(task),
		Drivers: by(DriverWorkerReport, DriverLiveness),
	},
	{
		ID: "T44", From: preparingReplan, To: failed, Types: types(tracked),
		Drivers: by(DriverWorkerReport, DriverLiveness),
	},
}
