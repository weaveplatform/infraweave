package acceptance

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerInitializationToolingReplay(parent *testing.T, subsets map[string]func(*testing.T, *sdk.Client) []string, workers **harness.FeatureReplay) {
	subsets["policy-initialization-tooling"] = func(t *testing.T, _ *sdk.Client) []string {
		if *workers == nil {
			*workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		contents, names := initializationToolingFeature(t)
		(*workers).Run(t, "C52", contents, names)
		return []string{"/exchanges/5/response/data/stack/run/state", "/exchanges/5/response/data/stack/run/history/0/note", "/exchanges/26/response/data/stacks"}
	}
}

func initializationToolingFeature(t *testing.T) ([]byte, []string) {
	t.Helper()
	var capture struct {
		Exchanges []struct {
			Request struct {
				Variables struct {
					Input *c48PolicyInput `json:"input"`
				} `json:"variables"`
			} `json:"request"`
		} `json:"exchanges"`
	}
	captureJSON(t, "C52", harness.ResponseFile, &capture)
	programs := make([]string, 2)
	for i, row := range []int{1, 20} {
		input := capture.Exchanges[row].Request.Variables.Input
		if input == nil {
			t.Fatal("C52 lacks captured policy source")
		}
		mapped := *input
		mapped.Space = "acme"
		mapped.Name = "C52 " + mapped.Name
		if i == 1 {
			mapped.Type = "TASK"
		}
		mapped.Body = strings.ReplaceAll(mapped.Body, `\n`, "\n")
		mapped.Body = strings.Replace(mapped.Body, "package spacelift", "package infraweave", 1)
		raw, err := json.Marshal(mapped)
		if err != nil {
			t.Fatal(err)
		}
		programs[i] = string(raw)
	}
	names := []string{
		"C52 denies assignment before worker preparation then executes an ordinary Terraform task",
		"C52 recorded V0 TASK denial follows the approved refusal-before-creation contract",
	}
	contents := captureWorkflowBackground() + fmt.Sprintf(`
  Scenario: %s
    Given token "h" sends "POST /api/v1/policies" with '%s' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{guard}" through B
    And it answers 200
    And the answer's id names "attachment"
    When token "h" sends "POST /api/v1/stacks/policy-replay/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, FAILED"
    And the answer's last state transition's note is "job assignment failed: denied by 1 initialization policy"
    And run "run" has 1 INITIALIZATION decisions and no worker preparation
    And the INITIALIZATION decisions of run "run" retain "capture-644-initialization-denied"
    When token "h" sends "DELETE /api/v1/policy-attachments/{attachment}" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/policy-replay/tasks" with '{"command":"terraform version","skipInitialization":true}' through B
    Then it answers 201
    And the task answer does not deprecate ordinary tasks
    And the answer's id names "task"
    And token "h" eventually reads the run "task" in the state "FINISHED" through A
    And token "h" reads the run "task"'s PERFORMING log through B, which holds "Terraform v{terraform.newest}" and not "Error"
    When token "h" sends "DELETE /api/v1/stacks/policy-replay" through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policies/{guard}" through B
    Then it answers 200
    And the capture's stack and policy inventories are restored

  Scenario: %s
    Given token "h" sends "POST /api/v1/policies" with '%s' through B
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{guard}" through A
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/policy-replay/tasks" with '{"command":"terraform version","skipInitialization":true}' through A
    Then it answers a 403 problem "forbidden"
    And the TASK answer carries its deprecation notice
    When token "h" sends "POST /api/v1/stacks/policy-replay/tasks" with '{"command":"terraform version","skipInitialization":false}' through B
    Then it answers a 403 problem "forbidden"
    And policy "guard" has 2 committed TASK refusals and no task creation effects
    When token "h" sends "DELETE /api/v1/stacks/policy-replay" through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policies/{guard}" through B
    Then it answers 200
    And the capture's stack and policy inventories are restored
`, names[0], programs[0], names[1], programs[1])
	return []byte(contents), names
}
