package feedback

import (
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCommitCheckStateMap(t *testing.T) {
	for state, want := range map[kernel.RunState]port.CheckState{
		kernel.RunStateQueued:          port.CheckQueued,
		kernel.RunStateReady:           port.CheckQueued,
		kernel.RunStatePreparing:       port.CheckInProgress,
		kernel.RunStateInitializing:    port.CheckInProgress,
		kernel.RunStatePlanning:        port.CheckInProgress,
		kernel.RunStateUnconfirmed:     port.CheckInProgress,
		kernel.RunStateConfirmed:       port.CheckInProgress,
		kernel.RunStatePreparingApply:  port.CheckInProgress,
		kernel.RunStateApplying:        port.CheckInProgress,
		kernel.RunStateReplanRequested: port.CheckInProgress,
		kernel.RunStatePreparingReplan: port.CheckInProgress,
		kernel.RunStatePendingReview:   port.CheckInProgress,
		kernel.RunStateFinished:        port.CheckSuccess,
		kernel.RunStateFailed:          port.CheckFailure,
		kernel.RunStateStopped:         port.CheckFailure,
		kernel.RunStateDiscarded:       port.CheckFailure,
		kernel.RunStateCanceled:        port.CheckCanceled,
		kernel.RunStateSkipped:         port.CheckSkipped,
	} {
		if got, ok := checkState(state, nil); !ok || got != want {
			t.Fatal("wrong check transition", state, got, ok)
		}
	}
	for _, state := range []kernel.RunState{
		kernel.RunStateUnknown,
		kernel.RunStatePerforming,
		kernel.RunStateDestroying, "untrusted",
	} {
		if _, ok := checkState(state, nil); ok {
			t.Fatal("unsupported run phase accepted", state)
		}
	}
	if got, _ := checkState(kernel.RunStateFinished, &kernel.PlanDelta{Resources: 9}); got != port.CheckNeutral {
		t.Fatal("known unchanged plan was not neutral", got)
	}
	if got, _ := checkState(kernel.RunStateFinished, &kernel.PlanDelta{Outputs: true}); got != port.CheckSuccess {
		t.Fatal("output-only change was treated as no-change", got)
	}
}

func TestUnitIndividualCheckOptions(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		state                    port.CheckState
		delta                    *kernel.PlanDelta
		failuresOnly, skip, want bool
	}{
		{"default queued", port.CheckQueued, nil, false, false, true},
		{"failure-only hides queued", port.CheckQueued, nil, true, false, false},
		{"failure-only hides success", port.CheckSuccess, &kernel.PlanDelta{Add: 1}, true, false, false},
		{"failure-only reports failed initialization", port.CheckFailure, nil, true, false, true},
		{"failure-only reports cancellation", port.CheckCanceled, nil, true, false, true},
		{"skip defers unknown plan", port.CheckQueued, nil, false, true, false},
		{"skip hides in-progress unknown plan", port.CheckInProgress, nil, false, true, false},
		{"skip hides finished no-change", port.CheckNeutral, &kernel.PlanDelta{Resources: 9}, false, true, false},
		{"skip reports changed plan", port.CheckSuccess, &kernel.PlanDelta{Add: 1}, false, true, true},
		{"skip reports output-only plan", port.CheckSuccess, &kernel.PlanDelta{Outputs: true}, false, true, true},
		{"skip preserves preplan failure", port.CheckFailure, nil, false, true, true},
		{"skip preserves cancellation after no-change", port.CheckCanceled, &kernel.PlanDelta{}, false, true, true},
		{"both options hide changed success", port.CheckSuccess, &kernel.PlanDelta{Add: 1}, true, true, false},
		{"both options retain failure", port.CheckFailure, &kernel.PlanDelta{}, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := individualCheck(tc.state, tc.delta, tc.failuresOnly, tc.skip); got != tc.want {
				t.Fatal("individual check option contract changed", got, tc.want)
			}
		})
	}
}

func TestUnitCheckChangeSummary(t *testing.T) {
	d := &kernel.PlanDelta{Add: 1, Change: 2, Delete: 3, Import: 4, Move: 5, Forget: 6, Resources: 90, Outputs: true}
	got := changeSummary(d)
	for _, part := range []string{"1 added", "2 changed", "3 deleted", "4 imported", "5 moved", "6 forgotten", "outputs changed: true"} {
		if !strings.Contains(got, part) {
			t.Fatal("summary lost change count", part, got)
		}
	}
	if strings.Contains(got, "90") || changeSummary(nil) == changeSummary(&kernel.PlanDelta{}) {
		t.Fatal("unplanned and no-change summaries are conflated")
	}
}
