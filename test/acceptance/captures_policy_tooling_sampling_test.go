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

func registerSamplingToolingReplay(parent *testing.T, subsets map[string]func(*testing.T, *sdk.Client) []string, workers **harness.FeatureReplay) {
	subsets["policy-sampling-tooling"] = func(t *testing.T, _ *sdk.Client) []string {
		if *workers == nil {
			*workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		contents, names := samplingToolingFeature(t)
		(*workers).Run(t, "C53", contents, names)
		return []string{
			"/exchanges/0/response/data/approval/kind",
			"/exchanges/7/response/errors/0/message",
			"/exchanges/13/response/data/planPolicy/sampleCount",
			"/exchanges/18/response/data/planPolicy/sampleCount",
			"/exchanges/30/response/data/policies",
		}
	}
}

func samplingToolingFeature(t *testing.T) ([]byte, []string) {
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
	captureJSON(t, "C53", harness.ResponseFile, &capture)
	programs := make([]string, 4)
	for i, row := range []int{2, 3, 15, 16} {
		input := capture.Exchanges[row].Request.Variables.Input
		if input == nil {
			t.Fatal("C53 lacks captured sample source")
		}
		body := strings.ReplaceAll(input.Body, `\n`, "\n")
		body = strings.Replace(body, "package spacelift", "package infraweave", 1)
		mapped := map[string]string{"name": "C53 " + input.Name, "body": body, "engineType": input.Engine}
		if i < 2 {
			mapped["type"], mapped["space"] = input.Type, "acme"
		}
		raw, err := json.Marshal(mapped)
		if err != nil {
			t.Fatal(err)
		}
		programs[i] = string(raw)
	}
	background := strings.Replace(captureWorkflowBackground(), `"workerPool":"{pool}","vendorConfig"`, `"workerPool":"{pool}","autodeploy":true,"vendorConfig"`, 1)
	background = strings.Replace(
		background,
		`resource "terraform_data" "this" {\n  input = "Policy replay"\n}`,
		`variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "this" {\n  input = var.secret\n}`,
		1,
	)
	name := "C53 retains a real sensitive Terraform capture with sample false then independently publishes samples"
	contents := background + fmt.Sprintf(`
  Scenario: %s
    Given token "h" sends "POST /api/v1/stacks/policy-replay/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"native-secret-666","writeOnly":true}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '%s' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "POST /api/v1/policies" with '%s' through B
    And it answers 201
    And the answer's id names "approval"
    And token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{guard}" through A
    And it answers 200
    And token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{approval}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/policy-replay/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "first"
    And token "h" eventually reads the run "first" in the state "FINISHED" through B
    And token "h" reads the PLAN page of policy "guard" with 1 records and 0 samples, masking "native-secret-666", through A
    And token "h" reads a PLAN input capture in space "acme" masking "native-secret-666" through B
    And token "h" reads an APPROVAL input capture in space "acme" masking "" through A
    Given the member key is exchanged through B for token "m"
    And the member key is space-writer on the space "acme"
    Then token "m" reads safe PLAN capture metadata in space "acme" but cannot open its input through A
    When token "h" sends "PUT /api/v1/policies/{guard}" with '%s' through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/policies/{approval}" with '%s' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/policy-replay/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through B
    Then it answers 200
    And the answer's id names "second"
    And token "h" eventually reads the run "second" in the state "FINISHED" through A
    And token "h" reads the PLAN page of policy "guard" with 2 records and 1 samples, masking "native-secret-666", through B
    And token "h" reads a PLAN input capture in space "acme" masking "native-secret-666" through A
    And token "m" cannot open the raw sample of policy "guard" through B
    And no object in the scenario's bucket holds "native-secret-666", of at least 3 objects
    When all retained public policy evidence expires
    Then token "h" reads the PLAN page of policy "guard" with 2 records and 0 samples, masking "native-secret-666", through A
    When token "h" sends "GET /api/v1/policy-input-captures?space=acme&type=PLAN" through B
    Then it answers 200
    And the answer's items is '[]'
    When token "h" sends "DELETE /api/v1/stacks/policy-replay" through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policies/{guard}" through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policies/{approval}" through A
    Then it answers 200
    And the capture's stack and policy inventories are restored
`, name, programs[0], programs[1], programs[2], programs[3])
	return []byte(contents), []string{name}
}
