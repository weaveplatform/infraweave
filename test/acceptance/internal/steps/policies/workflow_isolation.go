package policies

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerWorkflowIsolation(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the workflow fixture reviews eight independent runs in concurrent pairs on both replicas$`,
		func(ctx context.Context) error { return workflowIsolation(ctx, w) })
}

func workflowIsolation(ctx context.Context, w *harness.World) error {
	const count = 8
	calls := make([]raceRequest, count)
	runs := make([]string, count)
	for i := range count {
		stack := fmt.Sprintf("isolated%d", i)
		created, err := w.Do(ctx, "A", "h", http.MethodPost, "/api/v1/stacks", map[string]any{
			"input": map[string]any{
				"name": stack, "repository": "infra", "branch": "main", "provider": "GIT",
				"repositoryURL": w.IDs["infra.url"], "space": "legacy",
			}, "manageState": false,
		})
		if err != nil {
			return err
		}
		if created.Status != http.StatusCreated {
			return fmt.Errorf("%w: isolated stack create answered %d", harness.ErrStep, created.Status)
		}
		for _, policy := range []string{"One", "Two"} {
			attached, err := w.Do(ctx, "B", "h", http.MethodPut, "/api/v1/stacks/"+stack+"/policies/"+w.IDs[policy], nil)
			if err != nil {
				return err
			}
			if attached.Status != http.StatusOK {
				return fmt.Errorf("%w: isolated policy attach answered %d", harness.ErrStep, attached.Status)
			}
		}
		created, err = w.Do(ctx, "A", "h", http.MethodPost, "/api/v1/stacks/"+stack+"/runs", map[string]any{"runType": "PROPOSED", "commitSha": w.IDs["seed"]})
		if err != nil {
			return err
		}
		var run api.Run
		if created.Status != http.StatusOK || json.Unmarshal(created.Body, &run) != nil || run.Id == "" || run.State != api.RunStateQUEUED {
			return fmt.Errorf("%w: isolated run did not start at the approval gate", harness.ErrStep)
		}
		runs[i] = run.Id
		replica := "A"
		if i/2%2 != 0 {
			replica = "B"
		}
		client, err := w.Client(replica, w.Tokens["h"])
		if err != nil {
			return err
		}
		calls[i] = raceRequest{
			client: client,
			method: http.MethodPost,
			path:   "/api/v1/stacks/" + stack + "/runs/review",
			body:   map[string]any{"run": run.Id, "decision": "APPROVE"},
		}
	}
	// Each pair hits the same process and prepared-query cache concurrently.
	// Separate stack owners allow simultaneous evaluations. Bound the writer
	// contention independently of this input/helper/print isolation assertion.
	for i := 0; i < count; i += 2 {
		if _, err := raceRequests(ctx, calls[i:i+2]); err != nil {
			return err
		}
	}
	for i, id := range runs {
		if err := workflowIsolatedRun(ctx, w, fmt.Sprintf("isolated%d", i), id); err != nil {
			return err
		}
	}
	for _, server := range w.Servers {
		if bytes.Contains(server.Stdout(), []byte("private-policy-print-654")) {
			return fmt.Errorf("%w: private workflow print escaped to process logs", harness.ErrStep)
		}
	}
	return nil
}

func workflowIsolatedRun(ctx context.Context, w *harness.World, name, id string) error {
	response, err := w.Do(ctx, "B", "h", http.MethodGet, "/api/v1/runs/"+id, nil)
	if err != nil {
		return err
	}
	var run api.Run
	want := []string{"one:" + id, "two:" + id}
	if response.Status != http.StatusOK || json.Unmarshal(response.Body, &run) != nil || run.State != api.RunStateREADY || run.Flags == nil || !slices.Equal(*run.Flags, want) ||
		len(run.PolicyReceipts) < 2 || bytes.Contains(response.Body, []byte("private-policy-print-654")) {
		return fmt.Errorf("%w: concurrent run leaked another input, helper or print state", harness.ErrStep)
	}
	for i, receipt := range run.PolicyReceipts[len(run.PolicyReceipts)-2:] {
		policy, engine := "One", api.REGOV1
		if i == 1 {
			policy, engine = "Two", api.REGOV0
		}
		if receipt.PolicyName != policy || receipt.EngineType != engine || receipt.Error != nil || receipt.Outcome != "APPROVE" || !slices.Equal(receipt.Flags, want[i:i+1]) {
			return fmt.Errorf("%w: concurrent program lost its own decision and dialect", harness.ErrStep)
		}
	}
	var owners int
	if err := w.Row(ctx, `SELECT count(*) FROM run_flags WHERE account_id=$1 AND run_id=$2
 AND ((policy_id=$3 AND flag=$5) OR (policy_id=$4 AND flag=$6))`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(id), harness.UUIDOf(w.IDs["One"]), harness.UUIDOf(w.IDs["Two"]), want[0], want[1]}, &owners); err != nil {
		return err
	}
	if owners != 2 {
		return fmt.Errorf("%w: concurrent workflow did not retain both real flag owners", harness.ErrStep)
	}
	w.IDs[name] = id
	return workflowUpdate(ctx, w, name)
}
