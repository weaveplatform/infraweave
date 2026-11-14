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

// TestUnitAllowedStopKill lists stop for a run that initializes or plans
// until one is asked of it, then kill while a worker holds it until one is
// asked too, each needing its blocking variant for the run that holds its
// stack's lock.
func TestUnitAllowedStopKill(t *testing.T) {
	all := []accessapi.Action{
		accessapi.ActionRunStop, accessapi.ActionRunStopBlocking, accessapi.ActionRunKill,
		accessapi.ActionRunKillBlocking,
	}
	worker, asked := kernel.NewULID(), &lifecycle.Signal{}
	for _, c := range []struct {
		name       string
		typ        kernel.RunType
		state      kernel.RunState
		stop, kill *lifecycle.Signal
		blocking   bool
		want       []accessapi.Action
	}{
		{
			name: "planning", typ: kernel.RunTypeProposed, state: kernel.RunStatePlanning,
			want: []accessapi.Action{accessapi.ActionRunStop},
		},
		{
			name: "planning, blocking", typ: kernel.RunTypeTracked,
			state: kernel.RunStatePlanning, blocking: true,
			want: []accessapi.Action{accessapi.ActionRunStopBlocking},
		},
		{
			name: "a test case planning", typ: kernel.RunTypeTesting,
			state: kernel.RunStatePlanning, want: []accessapi.Action{},
		},
		{
			name: "stopping", typ: kernel.RunTypeTracked, state: kernel.RunStateInitializing,
			stop: asked, want: []accessapi.Action{accessapi.ActionRunKill},
		},
		{
			name: "stopping, blocking", typ: kernel.RunTypeTask,
			state: kernel.RunStatePerforming, stop: asked, blocking: true,
			want: []accessapi.Action{accessapi.ActionRunKillBlocking},
		},
		{
			name: "killed", typ: kernel.RunTypeTracked, state: kernel.RunStatePlanning,
			stop: asked, kill: asked, want: []accessapi.Action{},
		},
		{
			name: "stopped", typ: kernel.RunTypeTracked, state: kernel.RunStateStopped,
			stop: asked, want: []accessapi.Action{},
		},
	} {
		r := lifecycle.Run{
			Type: c.typ, State: c.state, Worker: &worker, Stop: c.stop, Kill: c.kill,
		}
		if got := read.Allowed(r, c.blocking, all); !slices.Equal(got, c.want) {
			t.Errorf("Allowed of %s = %v, want %v", c.name, got, c.want)
		}
	}
}
