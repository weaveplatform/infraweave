package policies

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerRuntime(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the runtime version matches the pinned OPA module$`, func() error {
		_, err := harness.PolicyRuntimeVersion(w.Last().Body)
		return err
	})
}
