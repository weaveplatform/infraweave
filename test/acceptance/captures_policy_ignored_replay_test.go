package acceptance

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

const ignoredCaptureCleanup = "C46 cleans its local stack and policy while preserving upstream handoffs"

func registerIgnoredCaptureReplay(parent *testing.T, subsets map[string]func(*testing.T, *sdk.Client) []string, workers **harness.FeatureReplay) {
	subsets["policy-ignored-history-cleanup"] = func(t *testing.T, _ *sdk.Client) []string {
		if *workers == nil {
			*workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		body, err := json.Marshal(capturedWorkflowPolicy(t, "C46"))
		if err != nil {
			t.Fatal(err)
		}
		feature := captureWorkflowBackground() + fmt.Sprintf(`    And token "h" sends "POST /api/v1/policies" with '%s' through A
    And it answers 201
    And the answer's id names "ignoredPolicy"
    And token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{ignoredPolicy}" through B
    And it answers 200

  Scenario: %s
    When token "h" sends "DELETE /api/v1/stacks/policy-replay" through A
    Then it answers 200
    And token "h" sends "GET /api/v1/stacks/policy-replay" through B
    And it answers 404
    And token "h" sends "DELETE /api/v1/policies/{ignoredPolicy}" through A
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{ignoredPolicy}" through B
    And it answers 404
    And the capture's stack and policy inventories are restored
`, body, ignoredCaptureCleanup)
		(*workers).Run(t, "C46", []byte(feature), []string{ignoredCaptureCleanup})
		return []string{"/exchanges/9/response/data/stacks", "/exchanges/9/response/data/policies"}
	}
}
