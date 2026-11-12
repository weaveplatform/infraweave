package auth

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the auth area's steps on sc, each step group sharing
// the scenario's World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&authSteps{w: w}).register(sc)
	keys := &keySteps{w: w, keys: map[string]*createdKey{}}
	keys.register(sc)
	keys.registerPersonal(sc)
	(&browserSteps{w: w}).register(sc)
	(&oauthSteps{w: w}).register(sc)
	(&kmsSteps{w: w}).register(sc)
}
