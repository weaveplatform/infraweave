package read_test

import (
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// TestUnitAllowed lists a tracked run's confirm and discard only while it
// waits for confirmation, each to a caller who holds it, and nothing once
// the run is discarded.
func TestUnitAllowed(t *testing.T) {
	all := []accessapi.Action{
		accessapi.ActionRunConfirm, accessapi.ActionRunDiscard, accessapi.ActionRunTargetedReplan,
		accessapi.ActionRunPrioritizeSet, accessapi.ActionRunCancel,
	}
	for _, c := range []struct {
		state kernel.RunState
		held  []accessapi.Action
		want  []accessapi.Action
	}{
		{kernel.RunStateUnconfirmed, all, []accessapi.Action{
			accessapi.ActionRunConfirm, accessapi.ActionRunDiscard,
			accessapi.ActionRunTargetedReplan, accessapi.ActionRunPrioritizeSet,
		}},
		{
			kernel.RunStateUnconfirmed,
			[]accessapi.Action{accessapi.ActionRunDiscard},
			[]accessapi.Action{accessapi.ActionRunDiscard},
		},
		{kernel.RunStateConfirmed, all, []accessapi.Action{accessapi.ActionRunPrioritizeSet}},
		{kernel.RunStateDiscarded, all, []accessapi.Action{}},
	} {
		r := lifecycle.Run{Type: kernel.RunTypeTracked, State: c.state}
		if got := read.Allowed(r, true, c.held); !slices.Equal(got, c.want) {
			t.Errorf("Allowed of %s holding %v = %v, want %v", c.state, c.held, got, c.want)
		}
	}
}
