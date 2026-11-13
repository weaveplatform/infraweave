package runs

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the runs area's steps on sc, sharing the scenario's
// World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&runSteps{w: w}).register(sc)
	(&logSteps{w: w}).register(sc)
	(&previewSteps{w: w, archives: map[string][]byte{}}).register(sc)
}
