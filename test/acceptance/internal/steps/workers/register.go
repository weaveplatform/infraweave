package workers

import (
	"crypto"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the workers area's steps on sc, each step group
// sharing the scenario's World w. The worker and launcher steps share the
// signing keys of the workers a scenario registers, by name.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	keys := map[string]crypto.Signer{}
	(&workerSteps{w: w, keys: keys}).register(sc)
	(&launcherSteps{w: w, keys: keys}).register(sc)
}
