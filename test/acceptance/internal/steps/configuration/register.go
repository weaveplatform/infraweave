package configuration

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the configuration area's steps on sc, sharing the
// scenario's World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&configSteps{w: w}).register(sc)
}
