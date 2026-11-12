package vendors

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the vendors area's steps on sc, sharing the
// scenario's World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&catalogSteps{w: w}).register(sc)
}
