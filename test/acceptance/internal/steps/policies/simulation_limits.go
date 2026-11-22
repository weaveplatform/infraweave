package policies

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerSimulationLimits(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^simulation boundary tests use a 32 MiB HTTP envelope$`, func() {
		w.Settings["server"] = map[string]any{
			"listen":         "127.0.0.1:0",
			"admin_listen":   "127.0.0.1:0",
			"public_url":     harness.PublicURL,
			"max_body_bytes": 32 << 20,
			"shutdown":       map[string]any{"ready_delay": "0s"},
		}
	})
	sc.Step(`^the PLAN simulation deadline is (\d+) milliseconds$`, func(ms int) {
		w.Settings["policy"] = map[string]any{"timeouts": map[string]any{"plan": fmt.Sprintf("%dms", ms)}}
	})
	sc.Step(`^token "(\w+)" simulates a (body|input) of (\d+) bytes through ([AB])$`, func(ctx context.Context, token, field string, size int, replica string) error {
		in := api.PolicySimulateInput{Type: api.PolicyTypePLAN, Body: "package infraweave\n", Input: "{}"}
		if field == "body" {
			prefix := "package infraweave\n#"
			if size < len(prefix) {
				return fmt.Errorf("%w: invalid simulation source fixture size", harness.ErrStep)
			}
			in.Body = prefix + strings.Repeat("x", size-len(prefix))
		} else {
			prefix, suffix := `{"value":"`, `"}`
			if size < len(prefix)+len(suffix) {
				return fmt.Errorf("%w: invalid simulation input fixture size", harness.ErrStep)
			}
			in.Input = prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
		}
		_, err := w.Do(ctx, replica, token, "POST", "/api/v1/policies/simulate", in)
		return err
	})
	sc.Step(`^an expensive PLAN simulation is attempted through ([AB])$`, func(ctx context.Context, replica string) error {
		in := api.PolicySimulateInput{Type: api.PolicyTypePLAN, Input: "{}", Body: `package infraweave
values := numbers.range(0,299)
warn contains "never" if {
  some a in values
  some b in values
  some c in values
  some d in values
  a+b+c+d == -1
}`}
		_, err := w.Do(ctx, replica, "h", "POST", "/api/v1/policies/simulate", in)
		return err
	})
}
