package feedback

import (
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitDeploymentState(t *testing.T) {
	t.Parallel()
	want := map[kernel.RunState]port.DeploymentState{
		kernel.RunStateUnconfirmed:    port.DeploymentPending,
		kernel.RunStateConfirmed:      port.DeploymentQueued,
		kernel.RunStatePreparingApply: port.DeploymentQueued,
		kernel.RunStateApplying:       port.DeploymentInProgress,
		kernel.RunStateFinished:       port.DeploymentSuccess,
		kernel.RunStateFailed:         port.DeploymentFailure,
		kernel.RunStateCanceled:       port.DeploymentCanceled,
		kernel.RunStateDiscarded:      port.DeploymentCanceled,
		kernel.RunStateStopped:        port.DeploymentCanceled,
		kernel.RunStateSkipped:        port.DeploymentCanceled,
	}
	for _, state := range kernel.RunStates() {
		got, ok := deploymentState(state)
		expected, mapped := want[state]
		if got != expected || ok != mapped {
			t.Errorf("%s maps to %s,%t; want %s,%t", state, got, ok, expected, mapped)
		}
	}
}

func TestUnitDeploymentReached(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		states []kernel.RunState
		want   bool
	}{
		{"no history", nil, false},
		{"no changes", []kernel.RunState{kernel.RunStateQueued, kernel.RunStatePlanning, kernel.RunStateFinished}, false},
		{"planning failure", []kernel.RunState{kernel.RunStatePlanning, kernel.RunStateFailed}, false},
		{"waiting for confirmation", []kernel.RunState{kernel.RunStatePlanning, kernel.RunStateUnconfirmed}, true},
		{"autodeploy", []kernel.RunState{kernel.RunStatePlanning, kernel.RunStateApplying, kernel.RunStateFinished}, true},
		{"discard after changes", []kernel.RunState{kernel.RunStateUnconfirmed, kernel.RunStateDiscarded}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var history runsapi.RunHistory
			for i, state := range tc.states {
				history.Transitions = append(history.Transitions, runsapi.RunTransition{Sequence: int64(i + 1), State: state})
			}
			if deploymentReached(history) != tc.want {
				t.Fatalf("historical eligibility differs for %v", tc.states)
			}
		})
	}
}

func TestUnitDeploymentEnvironment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, slug string
		labels     []string
		want       string
		enabled    bool
	}{
		{"default", "infra", nil, "infraweave/infra", true},
		{"override", "infra", []string{"team:infra", "ghenv:prod"}, "prod", true},
		{"disabled", "infra", []string{"ghenv:-"}, "", false},
		{"disable wins", "infra", []string{"ghenv:prod", "ghenv:-"}, "", false},
		{"empty override", "infra", []string{"ghenv:"}, "infraweave/infra", true},
		{"stable conflicting labels", "infra", []string{"ghenv:qa", "ghenv:prod"}, "prod", true},
		{"missing stack", "", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := slices.Clone(tc.labels)
			got, enabled := deploymentEnvironment(tc.slug, tc.labels)
			if got != tc.want || enabled != tc.enabled || !slices.Equal(before, tc.labels) {
				t.Fatalf("environment=%q enabled=%t; want %q,%t; labels=%v", got, enabled, tc.want, tc.enabled, tc.labels)
			}
		})
	}
}
