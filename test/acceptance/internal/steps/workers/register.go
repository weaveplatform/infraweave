package workers

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the workers area's steps on sc, each step group
// sharing the scenario's World w, whose Keys hold the pool keys the
// worker steps make and the launcher steps start launchers with.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&workerSteps{w: w}).register(sc)
	(&launcherSteps{w: w}).register(sc)
}
