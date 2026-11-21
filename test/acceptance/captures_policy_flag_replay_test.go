package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

var flagCaptureAssertions = []string{
	"/exchanges/0/response/data/policyCreatev2/id",
	"/exchanges/1/response/data/runTrigger/policyReceipts/0/flags",
	"/exchanges/3/response/data/stack/run/state",
	"/exchanges/6/response/data/stack/run/state",
	"/exchanges/7/response/data/stacks",
	"/exchanges/7/response/data/policies",
}

func registerFlagCaptureReplay(parent *testing.T, subsets map[string]func(*testing.T, *sdk.Client) []string, workers **harness.FeatureReplay) {
	subsets["policy-current-owner-reset"] = func(t *testing.T, _ *sdk.Client) []string {
		if *workers == nil {
			*workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		(*workers).Run(t, "C51", flagCaptureFeature(t), []string{"C51 resets its own queued flag before review rejection"})
		return slices.Clone(flagCaptureAssertions)
	}
}

func flagCaptureFeature(t *testing.T) []byte {
	t.Helper()
	input := capturedWorkflowPolicy(t, "C51")
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(captureWorkflowBackground() + fmt.Sprintf(`    And token "h" sends "POST /api/v1/policies" with '%s' through A
    And it answers 201
    And the answer's id names "resetPolicy"
    And token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{resetPolicy}" through B
    And it answers 200

  Scenario: C51 resets its own queued flag before review rejection
    When token "h" sends "POST /api/v1/stacks/policy-replay/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the run "run" retains C51's historical flag and its current reset
    When the C51 captured rejection reviews run "run" in "UNCONFIRMED"
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FAILED" through A
    And the run "run" retains C51's historical flag and its current reset
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    When token "h" sends "DELETE /api/v1/stacks/policy-replay" through A
    Then it answers 200
    And token "h" sends "GET /api/v1/stacks/policy-replay" through B
    And it answers 404
    And token "h" sends "DELETE /api/v1/policies/{resetPolicy}" through A
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{resetPolicy}" through B
    And it answers 404
    And the capture's stack and policy inventories are restored
`, body))
}

func capturedWorkflowPolicy(t *testing.T, id string) c48PolicyInput {
	t.Helper()
	var capture struct {
		Exchanges []struct {
			Request struct {
				Variables struct {
					Input c48PolicyInput `json:"input"`
				} `json:"variables"`
			} `json:"request"`
		} `json:"exchanges"`
	}
	captureJSON(t, id, harness.ResponseFile, &capture)
	input := capture.Exchanges[0].Request.Variables.Input
	if !strings.HasPrefix(input.Body, "package spacelift\n") {
		t.Fatalf("%s lacks its recorded policy source", id)
	}
	input.Name, input.Space = id+" "+input.Name, "acme"
	input.Body = strings.Replace(input.Body, "package spacelift\n", "package infraweave\n", 1)
	return input
}

func (s *captureWorkflowSteps) flagReset(ctx context.Context, name string) error {
	r, err := s.w.Do(ctx, "B", "h", http.MethodGet, "/api/v1/runs/"+s.w.IDs[name], nil)
	if err != nil {
		return err
	}
	var run api.Run
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &run) != nil || run.Flags == nil || len(*run.Flags) != 0 {
		return fmt.Errorf("%w: C51's current flag was not reset", harness.ErrStep)
	}
	flag := false
	for _, receipt := range run.PolicyReceipts {
		if receipt.PolicyName != "C51 policy-approval" || receipt.PolicyType != api.PolicyTypeAPPROVAL || receipt.Error != nil {
			return fmt.Errorf("%w: unexpected C51 policy receipt", harness.ErrStep)
		}
		flag = flag || slices.Contains(receipt.Flags, "capture-643-reset")
	}
	if !flag {
		return fmt.Errorf("%w: C51 lost its historical flag receipt", harness.ErrStep)
	}
	var owners, resets int
	if err := s.w.Row(ctx, `SELECT
		(SELECT count(*) FROM run_flags WHERE account_id=$1 AND run_id=$2),
		(SELECT count(*) FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2 AND policy_id=$3 AND reset_flags @> ARRAY['capture-643-reset'])`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(run.Id), harness.UUIDOf(s.w.IDs["resetPolicy"])}, &owners, &resets); err != nil {
		return err
	}
	if owners != 0 || resets == 0 {
		return fmt.Errorf("%w: C51 reset has owners=%d reset receipts=%d", harness.ErrStep, owners, resets)
	}
	return nil
}
