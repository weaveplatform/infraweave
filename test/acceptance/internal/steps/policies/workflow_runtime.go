package policies

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerWorkflowRuntime(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the workflow stores (\d+) messages of at most (\d+) bytes for policy "(\w+)" on run "(\w+)"$`,
		func(ctx context.Context, count, limit int, policy, run string) error {
			var raw []byte
			if err := w.Row(ctx, `SELECT decision FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2 AND policy_id=$3
 ORDER BY ordinal DESC LIMIT 1`, []any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[run]), harness.UUIDOf(w.IDs[policy])}, &raw); err != nil {
				return err
			}
			var decision struct {
				Notes []string `json:"approve_with_note"` //nolint:tagliatelle // Rego decisions retain the actual rule's name.
			}
			if err := json.Unmarshal(raw, &decision); err != nil {
				return fmt.Errorf("decode bounded workflow notes: %w", err)
			}
			if len(decision.Notes) != count {
				return fmt.Errorf("%w: bounded workflow stored %d notes, want %d", harness.ErrStep, len(decision.Notes), count)
			}
			for _, note := range decision.Notes {
				if len(note) > limit || len(note) < limit-3 || !utf8.ValidString(note) || !strings.Contains(note, "世") {
					return fmt.Errorf("%w: native workflow message lost its byte or UTF-8 bound", harness.ErrStep)
				}
			}
			marker := []byte("private-policy-print-654")
			if bytes.Contains(raw, marker) || bytes.Contains(w.Last().Body, marker) {
				return fmt.Errorf("%w: private print escaped into retained workflow evidence", harness.ErrStep)
			}
			for _, server := range w.Servers {
				if bytes.Contains(server.Stdout(), marker) {
					return fmt.Errorf("%w: private print escaped into process logs", harness.ErrStep)
				}
			}
			return nil
		})

	sc.Step(`^the workflow policy input budget is (\d+) bytes$`, func(limit int64) {
		w.Settings["policy"] = map[string]any{"max_input_bytes": limit}
	})

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
