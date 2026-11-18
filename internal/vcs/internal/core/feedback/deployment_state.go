package feedback

import (
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func deploymentState(state kernel.RunState) (port.DeploymentState, bool) {
	switch state {
	case kernel.RunStateUnconfirmed:
		return port.DeploymentPending, true
	case kernel.RunStateConfirmed, kernel.RunStatePreparingApply:
		return port.DeploymentQueued, true
	case kernel.RunStateApplying:
		return port.DeploymentInProgress, true
	case kernel.RunStateFinished:
		return port.DeploymentSuccess, true
	case kernel.RunStateFailed:
		return port.DeploymentFailure, true
	case kernel.RunStateCanceled, kernel.RunStateDiscarded, kernel.RunStateStopped, kernel.RunStateSkipped:
		return port.DeploymentCanceled, true
	}
	return "", false
}

func deploymentReached(history runsapi.RunHistory) bool {
	for _, step := range history.Transitions {
		if step.State == kernel.RunStateUnconfirmed || step.State == kernel.RunStateApplying {
			return true
		}
	}
	return false
}

func deploymentEnvironment(slug string, labels []string) (string, bool) {
	if slug == "" {
		return "", false
	}
	labels = slices.Clone(labels)
	slices.Sort(labels)
	environment, selected := "infraweave/"+slug, false
	for _, label := range labels {
		value, ok := strings.CutPrefix(label, "ghenv:")
		if !ok {
			continue
		}
		if value == "-" {
			return "", false
		}
		if value != "" && !selected {
			environment, selected = value, true
		}
	}
	return environment, true
}
