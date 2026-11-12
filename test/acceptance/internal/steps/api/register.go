package api

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the api area's steps on sc, each step group sharing
// the scenario's World w. The answer steps are every area's: they send the
// requests a scenario names and check what it was answered.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&answerSteps{w: w}).register(sc)
	(&servedSteps{w: w}).register(sc)
	(&deploySteps{}).register(sc)
	(&referenceSteps{}).register(sc)
	(&updateSteps{w: w, etags: map[string]string{}}).register(sc)
	(&errorSteps{w: w}).register(sc)
}
