package read_test

import (
	"slices"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
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
		if got := read.Allowed(r, true, c.held, read.Guards{}); !slices.Equal(got, c.want) {
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
		if got := read.Allowed(r, c.blocking, all, read.Guards{}); !slices.Equal(got, c.want) {
			t.Errorf("Allowed of %s = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestUnitGuards answers why a run cannot be retried or promoted: a retry
// takes a proposed or tracked run that ended other than in FINISHED,
// on a commit, a tracked run's still the stack's tracked commit; a promotion
// takes a proposed run on a commit newer than the stack's tracked commit,
// on a stack that allows it.
func TestUnitGuards(t *testing.T) {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	run := func(typ kernel.RunType, state kernel.RunState) lifecycle.Run {
		return lifecycle.Run{
			Type: typ, State: state, Commit: stacksapi.Commit{Hash: "abc", Timestamp: at},
		}
	}
	local := run(kernel.RunTypeProposed, kernel.RunStateFailed)
	local.LocalWorkspace = "workspace.tar.gz"
	tracked := stacksapi.Settings{TrackedCommit: &stacksapi.Commit{Hash: "abc"}}
	moved := stacksapi.Settings{TrackedCommit: &stacksapi.Commit{Hash: "next"}}
	for name, c := range map[string]struct {
		run      lifecycle.Run
		settings stacksapi.Settings
		want     string
	}{
		"a finished proposed run":                {run(kernel.RunTypeProposed, kernel.RunStateFinished), tracked, "a retry needs a run that ended other than in FINISHED"},
		"a finished tracked run":                 {run(kernel.RunTypeTracked, kernel.RunStateFinished), tracked, "a retry needs a run that ended other than in FINISHED"},
		"a failed tracked run":                   {run(kernel.RunTypeTracked, kernel.RunStateFailed), tracked, ""},
		"a discarded run":                        {run(kernel.RunTypeProposed, kernel.RunStateDiscarded), tracked, ""},
		"a stopped run":                          {run(kernel.RunTypeProposed, kernel.RunStateStopped), tracked, ""},
		"a canceled run":                         {run(kernel.RunTypeProposed, kernel.RunStateCanceled), tracked, ""},
		"an old tracked run":                     {run(kernel.RunTypeTracked, kernel.RunStateCanceled), moved, "a retry needs a tracked run on the stack's tracked commit"},
		"a tracked run without a tracked commit": {run(kernel.RunTypeTracked, kernel.RunStateFailed), stacksapi.Settings{}, "a retry needs a tracked run on the stack's tracked commit"},
		"an old proposed run":                    {run(kernel.RunTypeProposed, kernel.RunStateFailed), moved, ""},
		"a task":                                 {run(kernel.RunTypeTask, kernel.RunStateFinished), tracked, "a retry needs a proposed or tracked run"},
		"a planning run":                         {run(kernel.RunTypeTracked, kernel.RunStatePlanning), moved, "a retry needs a run that has ended"},
		"a local preview":                        {local, tracked, "a retry needs a run on a commit, not on a local workspace"},
	} {
		if got := read.RetryBlocker(c.run, c.settings); got != c.want {
			t.Errorf("RetryBlocker of %s = %q, want %q", name, got, c.want)
		}
	}

	allows := stacksapi.Settings{GithubActionDeploy: true}
	older := allows
	older.TrackedCommit = &stacksapi.Commit{Hash: "old", Timestamp: at.Add(-time.Second)}
	same := allows
	same.TrackedCommit = &stacksapi.Commit{Hash: "new", Timestamp: at}
	proposed := run(kernel.RunTypeProposed, kernel.RunStateFinished)
	for name, c := range map[string]struct {
		run      lifecycle.Run
		settings stacksapi.Settings
		want     string
	}{
		"with no tracked commit":   {proposed, allows, ""},
		"newer than the tracked":   {proposed, older, ""},
		"while it plans":           {run(kernel.RunTypeProposed, kernel.RunStatePlanning), older, ""},
		"as old as the tracked":    {proposed, same, "a promotion needs a commit newer than the stack's tracked commit"},
		"on a stack that does not": {proposed, stacksapi.Settings{}, "a promotion needs a stack that allows run promotion"},
		"of a tracked run":         {run(kernel.RunTypeTracked, kernel.RunStateFinished), allows, "a promotion needs a proposed run"},
		"of a local preview":       {local, allows, "a promotion needs a run on a commit, not on a local workspace"},
	} {
		if got := read.PromoteBlocker(c.run, c.settings); got != c.want {
			t.Errorf("PromoteBlocker %s = %q, want %q", name, got, c.want)
		}
	}
}

// TestUnitAllowedRetryPromote lists comment for any run, retry and
// promote only when nothing guards them, and a tracked run's retry as its
// blocking variant.
func TestUnitAllowedRetryPromote(t *testing.T) {
	all := []accessapi.Action{
		accessapi.ActionRunRetry, accessapi.ActionRunRetryBlocking, accessapi.ActionRunPromote,
		accessapi.ActionRunComment,
	}
	guarded := read.Guards{Retry: "no", Promote: "no"}
	for _, c := range []struct {
		name string
		typ  kernel.RunType
		g    read.Guards
		held []accessapi.Action
		want []accessapi.Action
	}{
		{
			name: "a proposed run", typ: kernel.RunTypeProposed, held: all,
			want: []accessapi.Action{
				accessapi.ActionRunRetry, accessapi.ActionRunPromote, accessapi.ActionRunComment,
			},
		},
		{
			name: "a tracked run", typ: kernel.RunTypeTracked, held: all, g: read.Guards{Promote: "no"},
			want: []accessapi.Action{accessapi.ActionRunRetryBlocking, accessapi.ActionRunComment},
		},
		{
			name: "a guarded run", typ: kernel.RunTypeProposed, held: all, g: guarded,
			want: []accessapi.Action{accessapi.ActionRunComment},
		},
		{
			name: "a reader", typ: kernel.RunTypeProposed, held: []accessapi.Action{},
			want: []accessapi.Action{},
		},
	} {
		r := lifecycle.Run{Type: c.typ, State: kernel.RunStateFinished}
		if got := read.Allowed(r, false, c.held, c.g); !slices.Equal(got, c.want) {
			t.Errorf("Allowed of %s = %v, want %v", c.name, got, c.want)
		}
	}
}
