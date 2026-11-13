package state

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the state area's steps on sc, sharing the scenario's
// World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&backendSteps{w: w}).register(sc)
}
