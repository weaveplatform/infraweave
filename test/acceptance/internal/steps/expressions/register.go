package expressions

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the expressions area's steps on sc, sharing the
// scenario's World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&expressionSteps{w: w}).register(sc)
}
