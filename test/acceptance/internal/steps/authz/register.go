package authz

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the authz area's steps on sc, each step group sharing
// the scenario's World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&spaceSteps{w: w}).register(sc)
	(&roleSteps{w: w}).register(sc)
	(&bindingSteps{w: w}).register(sc)
	(&grantViewSteps{w: w}).register(sc)
}
