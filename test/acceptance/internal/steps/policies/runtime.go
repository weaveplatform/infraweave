package policies

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
	"golang.org/x/mod/modfile"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerRuntime(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the runtime version matches the pinned OPA module$`, func() error {
		b, err := os.ReadFile(filepath.Join(harness.RepoRoot, "go.mod"))
		if err != nil {
			return fmt.Errorf("read runtime module pin: %w", err)
		}
		module, err := modfile.Parse("go.mod", b, nil)
		if err != nil {
			return fmt.Errorf("parse module pin: %w", err)
		}
		expected := ""
		for _, dependency := range module.Require {
			if dependency.Mod.Path == "github.com/open-policy-agent/opa" {
				expected = dependency.Mod.Version
			}
		}
		var version struct {
			OPA string `json:"openPolicyAgentVersion"`
		}
		if err := json.Unmarshal(w.Last().Body, &version); err != nil {
			return fmt.Errorf("decode runtime version: %w", err)
		}
		if expected == "" || version.OPA != expected {
			return fmt.Errorf("%w: runtime reports %q, linked pin %q", harness.ErrStep, version.OPA, expected)
		}
		return nil
	})
}
