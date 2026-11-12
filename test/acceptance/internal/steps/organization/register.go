package organization

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the organization area's steps on sc, each step group
// sharing the scenario's World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&orgSteps{w: w, secrets: map[string]string{}}).register(sc)
	(&managedSteps{w: w}).register(sc)
	(&accountSteps{w: w}).register(sc)
	(&settingsSteps{w: w}).register(sc)
}
