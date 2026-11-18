package vcs

import (
	"context"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerOptionSteps(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^two infraweave servers, A and B, with multiple VCS integrations disabled$`, func(ctx context.Context) error {
		w.Settings["features"] = map[string]any{"disabled": []string{"multiple-vcs-integrations"}}
		w.Settings["auth"] = harness.OperatorAuth()
		return w.TwoReplicas(ctx)
	})
}
