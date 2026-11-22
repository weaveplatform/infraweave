package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitC291HasExecutableRESTReplay(t *testing.T) {
	captures, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := captureReplayRegistry(t)
	for _, capture := range captures {
		if capture.ID != "C291" {
			continue
		}
		if capture.ReplayAfter != "" || capture.ReplayWith == "" || registry[capture.ReplayWith] == nil {
			t.Fatalf("C291 has no executable REST rejection replay; still deferred to %s", capture.ReplayAfter)
		}
		return
	}
	t.Fatal("C291 capture is missing")
}

func captureReplayRegistry(parent *testing.T) (map[string]func(*testing.T, *sdk.Client), map[string]func(*testing.T, *sdk.Client) []string) {
	full, subsets := maps.Clone(statefulCaptureReplayers), maps.Clone(captureSubsetReplayers)
	var workers *harness.FeatureReplay
	full["policy-approval-rejections"] = func(t *testing.T, _ *sdk.Client) {
		if workers == nil {
			registered := append(slices.Clone(areas), registerCaptureWorkflow)
			workers = harness.NewFeatureReplay(parent, registered)
		}
		contents, names := approvalCaptureFeature(t)
		workers.Run(t, "C291", contents, names)
	}
	management := subsets["policy-free-tier-management"]
	subsets["policy-free-tier-management"] = func(t *testing.T, client *sdk.Client) []string {
		executed := management(t, client)
		verifyApprovalCapturePrograms(t)
		if workers == nil {
			workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		contents, names := approvalCaptureFeature(t)
		workers.Run(t, "C48", contents, names)
		return append(executed, c48Workflow)
	}
	registerFlagCaptureReplay(parent, subsets, &workers)
	registerIgnoredCaptureReplay(parent, subsets, &workers)
	registerP13bCaptureReplays(parent, full, subsets, &workers)
	full["policy-simulation"] = func(t *testing.T, _ *sdk.Client) {
		if workers == nil {
			workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		name := "C192 hashes the recorded values through the public simulator"
		contents := []byte(`@postgres @objectstore
Feature: Recorded policy simulation
  Scenario: ` + name + `
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the simulation's evaluation and workflow state is recorded
    When recorded policy simulations "C192" agree through the public endpoint
    Then the simulation leaves evaluation, workflow and object storage unchanged
`)
		workers.Run(t, "C192", contents, []string{name})
	}
	return full, subsets
}

func TestUnitC192HasExecutableRESTReplay(t *testing.T) {
	captures, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := captureReplayRegistry(t)
	for _, capture := range captures {
		if capture.ID != "C192" {
			continue
		}
		if capture.ReplayAfter != "" || capture.ReplayWith != "policy-simulation" || registry[capture.ReplayWith] == nil {
			t.Fatal("C192 is not wired to its public simulation replay")
		}
		return
	}
	t.Fatal("C192 capture is missing")
}

func approvalCaptureFeature(t *testing.T) ([]byte, []string) {
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
	captureJSON(t, "C291", harness.ResponseFile, &capture)
	programs := make([]string, 3)
	for i := range programs {
		input := capture.Exchanges[i].Request.Variables.Input
		if input == nil || !strings.HasPrefix(input.Body, "package spacelift\n") {
			t.Fatal("C291 lacks its recorded policy source")
		}
		mapped := *input
		mapped.Name, mapped.Space = "C291 "+input.Name, "acme"
		mapped.Body = strings.Replace(input.Body, "package spacelift\n", "package infraweave\n", 1)
		encoded, err := json.Marshal(mapped)
		if err != nil {
			t.Fatal(err)
		}
		programs[i] = string(encoded)
	}
	background := captureWorkflowBackground()
	for i, name := range []string{"approval", "warning", "always"} {
		background += fmt.Sprintf(`    And token "h" sends "POST /api/v1/policies" with '%s' through A
    And it answers 201
    And the answer's id names "%s"
    And token "h" sends "PUT /api/v1/stacks/policy-replay/policies/{%s}" through B
    And it answers 200
`, programs[i], name, name)
	}
	names := []string{"C291 rejects a tracked plan in UNCONFIRMED", "C291 rejects a warned proposal in PENDING_REVIEW"}
	for i, row := range []struct{ kind, state string }{{"TRACKED", "UNCONFIRMED"}, {"PROPOSED", "PENDING_REVIEW"}} {
		background += fmt.Sprintf(`
  Scenario: %s
    When token "h" sends "POST /api/v1/stacks/policy-replay/runs" with '{"commitSha":"{seed}","runType":"%s"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "%s" through B
    And the answer's plan policy outcomes are '[{"deny":[],"warn":["capture-643-warning"]}]'
    When the C291 captured rejection reviews run "run" in "%s"
    Then it answers 200
    And the answer's state is '"%s"'
    And token "h" eventually reads the run "run" in the state "FAILED" through A
    And the run "run" replays C291 rejection from "%s" with its latest mixed receipts
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    When token "h" sends "DELETE /api/v1/stacks/policy-replay" through A
    Then it answers 200
    And token "h" sends "GET /api/v1/stacks/policy-replay" through B
    And it answers 404
`, names[i], row.kind, row.state, row.state, row.state, row.state)
		for _, name := range []string{"approval", "warning", "always"} {
			background += fmt.Sprintf(`    When token "h" sends "DELETE /api/v1/policies/{%s}" through A
    Then it answers 200
    And token "h" sends "GET /api/v1/policies/{%s}" through B
    And it answers 404
`, name, name)
		}
		background += "    And the capture's stack and policy inventories are restored\n"
	}
	return []byte(background), names
}

func registerCaptureWorkflow(sc *godog.ScenarioContext, w *harness.World) {
	s := &captureWorkflowSteps{w: w}
	sc.Step(`^the capture's stack and policy inventories are kept$`, s.keep)
	sc.Step(`^the capture's stack and policy inventories are restored$`, s.restored)
	sc.Step(`^the C291 captured rejection reviews run "(\w+)" in "(UNCONFIRMED|PENDING_REVIEW)"$`, s.reject)
	sc.Step(`^the run "(\w+)" replays C291 rejection from "(UNCONFIRMED|PENDING_REVIEW)" with its latest mixed receipts$`, s.rejection)
	sc.Step(`^the C51 captured rejection reviews run "(\w+)" in "(UNCONFIRMED)"$`, func(ctx context.Context, name, state string) error {
		return s.reviewRecorded(ctx, "C51", name, state)
	})
	sc.Step(`^the run "(\w+)" retains C51's historical flag and its current reset$`, s.flagReset)
}

type captureWorkflowSteps struct {
	w      *harness.World
	before map[string][]string
}

func (s *captureWorkflowSteps) inventory(ctx context.Context, kind string) ([]string, error) {
	ids, seen, cursor := []string{}, map[string]bool{}, ""
	for {
		response, err := s.w.Do(ctx, "A", "h", http.MethodGet, "/api/v1/"+kind+"?limit=100&cursor="+url.QueryEscape(cursor), nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Next *string `json:"nextCursor"`
		}
		if response.Status != http.StatusOK || json.Unmarshal(response.Body, &page) != nil || page.Items == nil {
			return nil, fmt.Errorf("%w: capture inventory %s unreadable", harness.ErrStep, kind)
		}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if page.Next == nil {
			break
		}
		if *page.Next == "" || seen[*page.Next] {
			return nil, fmt.Errorf("%w: capture inventory cursor repeated", harness.ErrStep)
		}
		cursor, seen[*page.Next] = *page.Next, true
	}
	slices.Sort(ids)
	return ids, nil
}

func (s *captureWorkflowSteps) keep(ctx context.Context) error {
	s.before = map[string][]string{}
	for _, kind := range []string{"stacks", "policies"} {
		ids, err := s.inventory(ctx, kind)
		if err != nil {
			return err
		}
		s.before[kind] = ids
	}
	return nil
}

func (s *captureWorkflowSteps) restored(ctx context.Context) error {
	if len(s.before) != 2 {
		return fmt.Errorf("%w: capture cleanup has no initial inventory", harness.ErrStep)
	}
	for _, kind := range []string{"stacks", "policies"} {
		ids, err := s.inventory(ctx, kind)
		if err != nil {
			return err
		}
		if !slices.Equal(ids, s.before[kind]) {
			return fmt.Errorf("%w: capture left %s after public cleanup", harness.ErrStep, kind)
		}
	}
	return nil
}

func recordedWorkflow(id string) (workflowCapture, map[string]string, error) {
	var capture workflowCapture
	var reviews struct {
		Exchanges []struct {
			Response struct {
				Data struct {
					Review *struct {
						State string `json:"state"`
						Note  string `json:"note"`
					} `json:"runReview"`
				} `json:"data"`
			} `json:"response"`
		} `json:"exchanges"`
	}
	data, err := os.ReadFile(filepath.Join(capturesRoot, id, harness.ResponseFile))
	if err != nil {
		return capture, nil, fmt.Errorf("read C291 capture: %w", err)
	}
	if err := json.Unmarshal(data, &capture); err != nil {
		return capture, nil, fmt.Errorf("decode C291 workflow: %w", err)
	}
	if err := json.Unmarshal(data, &reviews); err != nil {
		return capture, nil, fmt.Errorf("decode C291 reviews: %w", err)
	}
	notes := map[string]string{}
	for _, exchange := range reviews.Exchanges {
		if review := exchange.Response.Data.Review; review != nil {
			notes[review.State] = review.Note
		}
	}
	return capture, notes, nil
}

func (s *captureWorkflowSteps) reject(ctx context.Context, name, state string) error {
	return s.reviewRecorded(ctx, "C291", name, state)
}

func (s *captureWorkflowSteps) reviewRecorded(ctx context.Context, captureID, name, state string) error {
	_, notes, err := recordedWorkflow(captureID)
	if err != nil {
		return err
	}
	response, err := s.w.Do(ctx, "A", "h", http.MethodGet, "/api/v1/runs/"+s.w.IDs[name], nil)
	if err != nil {
		return err
	}
	var run api.Run
	if response.Status != http.StatusOK || json.Unmarshal(response.Body, &run) != nil || string(run.State) != state || len(run.History) == 0 || notes[state] == "" {
		return fmt.Errorf("%w: C291 run is not at its captured review state", harness.ErrStep)
	}
	version := run.History[len(run.History)-1].StateVersion
	if version == nil {
		return fmt.Errorf("%w: C291 review lacks its current state fence", harness.ErrStep)
	}
	_, err = s.w.Do(ctx, "B", "h", http.MethodPost, "/api/v1/stacks/policy-replay/runs/review", map[string]any{
		"run": run.Id, "decision": "REJECT", "note": notes[state], "stateVersion": *version,
	})
	return err
}

func (s *captureWorkflowSteps) rejection(_ context.Context, name, state string) error {
	capture, _, err := recordedWorkflow("C291")
	if err != nil {
		return err
	}
	var expected *workflowRun
	for _, exchange := range capture.Exchanges {
		if stack := exchange.Response.Data.Stack; stack != nil && stack.Run != nil && len(stack.Run.History) >= 2 && stack.Run.State == "FAILED" &&
			stack.Run.History[1].State == state {
			expected = stack.Run
		}
	}
	var run api.Run
	if expected == nil || len(expected.Receipts) < 2 || json.Unmarshal(s.w.Last().Body, &run) != nil || run.Id != s.w.IDs[name] || len(run.History) < 2 {
		return fmt.Errorf("%w: C291 rejection lacks actual and captured run evidence", harness.ErrStep)
	}
	last, before := run.History[len(run.History)-1], run.History[len(run.History)-2]
	if string(run.State) != expected.State || string(before.State) != state || last.Note == nil || *last.Note != expected.History[0].Note {
		return fmt.Errorf("%w: C291 rejected state/history/note differs from capture", harness.ErrStep)
	}
	for _, event := range run.History {
		if event.State == api.RunStateAPPLYING {
			return fmt.Errorf("%w: rejected C291 plan was applied", harness.ErrStep)
		}
	}
	latest := map[string]api.RunPolicyReceipt{}
	for _, receipt := range run.PolicyReceipts {
		if receipt.PolicyType == api.PolicyTypeAPPROVAL && receipt.Id > latest[receipt.PolicyName].Id {
			latest[receipt.PolicyName] = receipt
		}
	}
	if len(latest) != 2 {
		return fmt.Errorf("%w: C291 needs two distinct current approval policy outcomes", harness.ErrStep)
	}
	identities := map[string]bool{}
	for _, recorded := range expected.Receipts[:2] {
		actual := latest["C291 "+recorded.PolicyID]
		if actual.Id == "" || actual.Outcome != strings.ToUpper(recorded.Outcome) || actual.Error != nil || actual.PolicyDeleted || actual.PolicySlug == nil ||
			identities[*actual.PolicySlug] {
			return fmt.Errorf("%w: C291 latest mixed receipt differs from captured outcome", harness.ErrStep)
		}
		identities[*actual.PolicySlug] = true
	}
	return nil
}

func captureWorkflowBackground() string {
	return `@terraform @postgres @objectstore @vcs @docker
Feature: Captured policy workflows
  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "Policy replay"\n} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And the capture's stack and policy inventories are kept
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Policy replay","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"acme","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "policy-replay" tracking the commit kept as "seed" through A
`
}
