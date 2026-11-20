package http

import (
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
)

func planOutcomesOf(outcomes []evidence.PlanOutcome) []api.PlanPoliciesOutcome {
	out := make([]api.PlanPoliciesOutcome, 0, len(outcomes))
	for _, result := range outcomes {
		out = append(out, api.PlanPoliciesOutcome{
			Id: result.ID.String(), CreatedAt: unix(result.CreatedAt.Unix()),
			Deny: result.Deny, Warn: result.Warn,
		})
	}
	return out
}
