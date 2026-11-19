package feedback

import (
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func checkState(state kernel.RunState, delta *kernel.PlanDelta) (port.CheckState, bool) {
	switch state {
	case kernel.RunStateQueued, kernel.RunStateReady:
		return port.CheckQueued, true
	case kernel.RunStatePreparing,
		kernel.RunStateInitializing,
		kernel.RunStatePlanning,
		kernel.RunStateUnconfirmed,
		kernel.RunStateConfirmed,
		kernel.RunStatePreparingApply,
		kernel.RunStateApplying,
		kernel.RunStateReplanRequested,
		kernel.RunStatePreparingReplan,
		kernel.RunStatePendingReview:
		return port.CheckInProgress, true
	case kernel.RunStateFinished:
		if delta != nil && !delta.Changes() {
			return port.CheckNeutral, true
		}
		return port.CheckSuccess, true
	case kernel.RunStateFailed, kernel.RunStateStopped, kernel.RunStateDiscarded:
		return port.CheckFailure, true
	case kernel.RunStateCanceled:
		return port.CheckCanceled, true
	case kernel.RunStateSkipped:
		return port.CheckSkipped, true
	default:
		return "", false
	}
}

func unsuccessful(state port.CheckState) bool {
	return state == port.CheckFailure || state == port.CheckCanceled
}

func individualCheck(state port.CheckState, delta *kernel.PlanDelta, failuresOnly, skipUnchanged bool) bool {
	if unsuccessful(state) {
		return true
	}
	if failuresOnly {
		return false
	}
	// No-change runs must leave no individual check, so this option defers
	// non-failure reports until the plan has established a nonempty delta.
	return !skipUnchanged || delta != nil && delta.Changes()
}

func changeSummary(delta *kernel.PlanDelta) string {
	if delta == nil {
		return "Change counts are not available yet."
	}
	return fmt.Sprintf(
		"%d added, %d changed, %d deleted, %d imported, %d moved, %d forgotten; outputs changed: %t.",
		delta.Add,
		delta.Change,
		delta.Delete,
		delta.Import,
		delta.Move,
		delta.Forget,
		delta.Outputs,
	)
}
