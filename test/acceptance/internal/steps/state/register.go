package state

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the state area's steps on sc, sharing the scenario's
// World w. The import steps read the runs the backend steps took over and
// the URLs the URL steps kept.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	b, u := &backendSteps{w: w}, &urlSteps{w: w}
	b.register(sc)
	(&workspaceSteps{w: w}).register(sc)
	u.register(sc)
	(&importSteps{w: w, backend: b, urls: u}).register(sc)
	(&rollbackSteps{w: w}).register(sc)
}
