package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerWorkflowFlags(sc *godog.ScenarioContext, w *harness.World) {
	registerWorkflowRecovery(sc, w)
	registerWorkflowRuntime(sc, w)
	sc.Step(`^the committed workflow update of run "(\w+)" matches the public policy snapshot$`,
		func(ctx context.Context, run string) error { return workflowUpdate(ctx, w, run) })
	sc.Step(`^token "(\w+)" eventually reads run "(\w+)" flags '([^']*)' through ([AB])$`,
		func(ctx context.Context, token, id, expected, replica string) error {
			var want []string
			if err := json.Unmarshal([]byte(w.Resolve(expected)), &want); err != nil {
				return fmt.Errorf("decode expected workflow flags: %w", err)
			}
			return deps.Eventually(ctx, "committed public workflow flags", func(ctx context.Context) (bool, error) {
				response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/runs/"+url.PathEscape(w.IDs[id]), nil)
				if err != nil {
					return false, err
				}
				if response.Status != http.StatusOK {
					return false, fmt.Errorf("%w: workflow run read answered %d", harness.ErrStep, response.Status)
				}
				var run api.Run
				if err := json.Unmarshal(response.Body, &run); err != nil {
					return false, fmt.Errorf("decode workflow flags: %w", err)
				}
				return run.Flags != nil && slices.Equal(*run.Flags, want), nil
			})
		})
}

func workflowUpdate(ctx context.Context, w *harness.World, id string) error {
	var view api.Run
	if err := json.Unmarshal(w.Last().Body, &view); err != nil {
		return fmt.Errorf("decode public workflow snapshot: %w", err)
	}
	if view.Id != w.IDs[id] || view.Flags == nil {
		return fmt.Errorf("%w: workflow snapshot names a different run or omits flags", harness.ErrStep)
	}
	var raw []byte
	if err := w.Row(ctx, `SELECT document FROM events
 WHERE account_id=$1 AND kind='run_updated' AND subject_kind='run' AND subject_id=$2
 ORDER BY occurred_at DESC,id DESC LIMIT 1`, []any{harness.UUIDOf(harness.Home.Account), w.IDs[id]}, &raw); err != nil {
		return err
	}
	var event runsapi.Updated
	if err := json.Unmarshal(raw, &event); err != nil {
		return fmt.Errorf("decode committed workflow update: %w", err)
	}
	if event.Run.ID.String() != view.Id || string(event.State) != string(view.State) || !slices.Equal(event.Run.Flags, *view.Flags) ||
		len(event.PolicyReceipts) < len(view.PolicyReceipts) {
		return fmt.Errorf("%w: committed workflow update lost public state, flags or receipt history", harness.ErrStep)
	}
	// A later automatic evaluation may already have appended receipts in the
	// same state. Its history must still preserve the public snapshot's prefix.
	for i, want := range view.PolicyReceipts {
		got := event.PolicyReceipts[i]
		if got.Type != string(want.PolicyType) || got.Name != want.PolicyName || got.Outcome != want.Outcome || !slices.Equal(got.Flags, want.Flags) {
			return fmt.Errorf("%w: committed workflow receipt differs from its public projection", harness.ErrStep)
		}
	}
	return publicWorkflowReceipts(raw)
}

func publicWorkflowReceipts(raw []byte) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode workflow receipt document: %w", err)
	}
	value, found := document["policy_receipts"]
	if !found {
		return nil
	}
	var receipts []map[string]json.RawMessage
	if err := json.Unmarshal(value, &receipts); err != nil {
		return fmt.Errorf("decode workflow receipt summaries: %w", err)
	}
	for _, receipt := range receipts {
		for field := range receipt {
			switch field {
			case "type", "name", "outcome", "flags":
			default:
				return fmt.Errorf("%w: workflow update exposes nonpublic receipt field %s", harness.ErrStep, field)
			}
		}
	}
	return nil
}
