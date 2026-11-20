package push

import (
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func inProgressFacts(current []runsapi.PushRunFacts) []policyapi.RunFacts {
	inProgress := make([]policyapi.RunFacts, 0, len(current))
	for _, run := range current {
		r := policyapi.RunFacts{
			ID:        run.ID,
			Type:      run.Type,
			State:     run.State,
			CreatedAt: run.CreatedAt,
			UpdatedAt: run.UpdatedAt,
			Branch: known(
				run.Branch,
			),
			BasedOnLocalWorkspace: known(run.BasedOnLocalWorkspace),
			DriftDetection:        known(run.DriftDetection),
			ForceApplyMode:        known(run.ForceApplyMode),
		}
		if run.Commit != nil {
			r.Commit = known(commitFacts(stacksapi.Commit(*run.Commit)))
		}
		if run.TriggeredBy != nil {
			r.TriggeredBy = known(*run.TriggeredBy)
		}
		if run.Command != nil {
			r.Command = known(*run.Command)
		}
		inProgress = append(inProgress, r)
	}
	return inProgress
}
