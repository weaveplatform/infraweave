package platform

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the platform area's steps on sc, each step group
// sharing the scenario's World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&startupSteps{w: w}).register(sc)
	(&observeSteps{w: w}).register(sc)
	(&queueSteps{w: w}).register(sc)
	(&queueStatSteps{w: w}).register(sc)
	(&secretSteps{w: w}).register(sc)
	(&signedURLSteps{w: w}).register(sc)
	(&purgeSteps{w: w, held: map[string][2]string{}}).register(sc)
	(&operatorSteps{w: w}).register(sc)
}
