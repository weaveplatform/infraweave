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

func registerPageToolingReplay(parent *testing.T, subsets map[string]func(*testing.T, *sdk.Client) []string, workers **harness.FeatureReplay) {
	subsets["policy-page-tooling"] = func(t *testing.T, _ *sdk.Client) []string {
		if *workers == nil {
			*workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		verifyPageLoginSources(t)
		sessions, sessionNames := loginToolingReplayFeature(t)
		(*workers).Run(t, "C273-sessions", sessions, sessionNames)
		contents, names := policyPageReplayFeature(t)
		(*workers).Run(t, "C273", contents, names)
		return []string{
			"/exchanges/7/response/data/accountUpdateAuthorizationScheme",
			"/exchanges/9/response/errors/0/message",
			"/exchanges/12/response/data/viewer",
			"/exchanges/13/response/data/accountUpdateAuthorizationScheme",
			"/exchanges/24/response/data/policy/type",
			"/exchanges/24/response/data/policy/engineType",
			"/exchanges/24/response/data/policy/sampleCount",
			"/exchanges/24/response/data/policy/hasAttachedStacks",
			"/exchanges/25/response/data/policy/evaluationRecords/0/outcome",
			"/exchanges/25/response/data/policy/evaluationRecords/1/outcome",
			"/exchanges/27/response/data/policySimulatev2",
			"/exchanges/30/response/data/policy/searchEvaluationRecordSuggestions/filteredCount",
			"/exchanges/32/response/data/policy/type",
			"/exchanges/32/response/data/policy/sampleCount",
			"/exchanges/32/response/data/policy/hasAttachedStacks",
			"/exchanges/33/response/data/policy/evaluationRecords",
			"/exchanges/34/response/data/stacks",
			"/exchanges/34/response/data/policies",
		}
	}
}

func verifyPageLoginSources(t *testing.T) {
	t.Helper()
	var page struct {
		Exchanges []struct {
			Request struct {
				Variables struct {
					Input struct {
						Body string `json:"body"`
					} `json:"input"`
				} `json:"variables"`
			} `json:"request"`
		} `json:"exchanges"`
	}
	var login struct {
		Exchanges []toolingExchange `json:"exchanges"`
	}
	captureJSON(t, "C273", harness.ResponseFile, &page)
	captureJSON(t, "C50", harness.ResponseFile, &login)
	for i, row := range []int{4, 11} {
		stage := []string{"C50-configure-login", "C50-login-flag-false-active-update"}[i]
		if page.Exchanges[row].Request.Variables.Input.Body != toolingUpdateBody(t, toolingStage(t, login.Exchanges, stage)) {
			t.Fatal("C273 no longer shares the actual C50 session-control programs")
		}
	}
}

func policyPageReplayFeature(t *testing.T) ([]byte, []string) {
	t.Helper()
	var capture struct {
		Exchanges []struct {
			Response struct {
				Data struct {
					Policy *c48PolicyInput `json:"policy"`
				} `json:"data"`
			} `json:"response"`
		} `json:"exchanges"`
	}
	captureJSON(t, "C273", harness.ResponseFile, &capture)
	programs := make([]string, 2)
	for i, row := range []int{24, 32} {
		if capture.Exchanges[row].Response.Data.Policy == nil {
			t.Fatal("C273 lacks the actual browser detail response")
		}
		mapped := *capture.Exchanges[row].Response.Data.Policy
		mapped.Space = "acme"
		mapped.Name = "C273 " + mapped.Name
		mapped.Body = strings.ReplaceAll(mapped.Body, `\n`, "\n")
		mapped.Body = strings.Replace(mapped.Body, "package spacelift", "package infraweave", 1)
		raw, err := json.Marshal(mapped)
		if err != nil {
			t.Fatal(err)
		}
		programs[i] = string(raw)
	}
	background := strings.Replace(captureWorkflowBackground(), `"workerPool":"{pool}","vendorConfig"`, `"workerPool":"{pool}","autodeploy":true,"vendorConfig"`, 1)
	name := "C273 public body used-by samples filters and simulation compose one real PLAN policy"
	contents := background + fmt.Sprintf(`
  Scenario: %s
    Given token "h" sends "POST /api/v1/policies" with '%s' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{guard}" through B
    And it answers 200
    And the answer's id names "attachment"
    When token "h" sends "POST /api/v1/stacks/policy-replay/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "first"
    And token "h" eventually reads the run "first" in the state "FINISHED" through B
    When token "h" sends "POST /api/v1/stacks/policy-replay/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through B
    Then it answers 200
    And the answer's id names "second"
    And token "h" eventually reads the run "second" in the state "FINISHED" through A
    And token "h" reads the PLAN page of policy "guard" with 2 records and 2 samples, masking "", through A
    And token "h" reads stack "policy-replay" and attachment "attachment" for policy "guard" with committed history through B
    When token "h" sends "GET /api/v1/policies" through B
    Then the policy page IDs are "{guard}"
    Given the simulation's evaluation and workflow state is recorded
    When recorded policy simulations "C273" agree through the public endpoint
    Then the simulation leaves evaluation, workflow and object storage unchanged
    When token "h" sends "POST /api/v1/policies" with '%s' through B
    Then it answers 201
    And the answer's id names "notification"
    And token "h" eventually reads policy "notification" with 0 visible attachments and empty evidence through A
    When token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{notification}" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "DELETE /api/v1/stacks/policy-replay" through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policies/{guard}" through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policies/{notification}" through A
    Then it answers 200
    And the capture's stack and policy inventories are restored
`, name, programs[0], programs[1])
	return []byte(contents), []string{name}
}
