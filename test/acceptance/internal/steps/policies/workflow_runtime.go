package policies

import (
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerWorkflowRuntime(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the workflow policies allow (\d+) ms per evaluation and (\d+) ms combined$`, func(each, combined int) {
		w.Settings["policy"] = map[string]any{"timeouts": map[string]any{
			"approval": fmt.Sprintf("%dms", each), "plan": fmt.Sprintf("%dms", each), "combined": fmt.Sprintf("%dms", combined),
		}}
	})
	sc.Step(`^the answer contains a workflow "(APPROVAL|PLAN|GIT_PUSH|TRIGGER)" error "([A-Z_]+)"$`, func(kind, code string) error {
		var run api.Run
		if err := json.Unmarshal(w.Last().Body, &run); err != nil {
			return fmt.Errorf("decode workflow failure receipt: %w", err)
		}
		for _, receipt := range run.PolicyReceipts {
			if string(receipt.PolicyType) == kind && receipt.Error != nil && receipt.Error.Code == code && receipt.Error.Message != "" {
				return nil
			}
		}
		return fmt.Errorf("%w: no committed %s %s error receipt", harness.ErrStep, kind, code)
	})
}
