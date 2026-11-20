package policies

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type planMessages struct {
	Deny []string `json:"deny"`
	Warn []string `json:"warn"`
}

func registerPlan(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the answer's plan policy outcomes are '([^']*)'$`, func(expected string) error {
		var run api.Run
		if err := json.Unmarshal(w.Last().Body, &run); err != nil {
			return fmt.Errorf("decode public plan outcomes: %w", err)
		}
		var want []planMessages
		if err := json.Unmarshal([]byte(expected), &want); err != nil {
			return fmt.Errorf("decode expected plan messages: %w", err)
		}
		got := make([]planMessages, 0, len(run.PlanPoliciesOutcomes))
		for _, outcome := range run.PlanPoliciesOutcomes {
			if _, err := kernel.ParseULID(outcome.Id); err != nil || outcome.CreatedAt <= 0 {
				return fmt.Errorf("%w: plan outcome lacks a committed identity or timestamp", harness.ErrStep)
			}
			got = append(got, planMessages{Deny: outcome.Deny, Warn: outcome.Warn})
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("%w: public plan messages are %+v, want %+v", harness.ErrStep, got, want)
		}
		return nil
	})
}
