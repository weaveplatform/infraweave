package tiers

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the tiers area's steps on sc, each step group sharing
// the scenario's World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&platformSteps{w: w}).register(sc)
	(&featureSteps{w: w}).register(sc)
}
