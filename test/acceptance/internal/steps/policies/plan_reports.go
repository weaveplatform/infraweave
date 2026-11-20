package policies

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerPlanReports(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(
		`^token "(\w+)" records the protocol plan "(valid|malformed|unreadable|missing-changes|invalid-version)" with artifact key "(\w+)" for run "(\w+)" in state version (\d+) through ([AB])$`,
		func(ctx context.Context, token, fault, key, run string, version int64, replica string) error {
			id, err := kernel.ParseULID(w.IDs[run])
			if err != nil {
				return err
			}
			account, err := kernel.ParseAccountID(harness.Home.Account)
			if err != nil {
				return err
			}
			artifact, err := hex.DecodeString(w.IDs[key])
			if err != nil {
				return err
			}
			// These deliberately synthetic documents model a buggy or malicious
			// authenticated worker. Native tool plans have separate acceptance.
			document := []byte(`{"format_version":"1.2","terraform_version":"1.15.0","resource_changes":[]}`)
			switch fault {
			case "malformed":
				document = []byte(`{`)
			case "invalid-version":
				document = []byte(`{"format_version":"0.9","resource_changes":[]}`)
			}
			if fault != "missing-changes" {
				var changes bytes.Buffer
				if err := plan.Write(&changes, nil); err != nil {
					return err
				}
				if err := w.RecordArtifact(ctx, replica, token, account, id, kernel.RunStatePlanning, version, "changes.json", artifact, changes.Bytes()); err != nil {
					return err
				}
			}
			if fault == "unreadable" {
				// The upload checksum is valid, but the authenticated worker has
				// sealed its document under a different content key.
				artifact = bytes.Repeat([]byte{0xa5}, 32)
			}
			return w.RecordArtifact(ctx, replica, token, account, id, kernel.RunStatePlanning, version, "plan.json", artifact, document)
		},
	)
	sc.Step(`^the answer's PLAN receipt outcomes are "([^"]*)"$`, func(want string) error {
		var run api.Run
		if err := json.Unmarshal(w.Last().Body, &run); err != nil {
			return fmt.Errorf("decode plan report receipts: %w", err)
		}
		values := []string{}
		for _, receipt := range run.PolicyReceipts {
			if receipt.PolicyType != api.PolicyTypePLAN {
				continue
			}
			values = append(values, receipt.Outcome)
		}
		if strings.Join(values, ", ") != want {
			return fmt.Errorf("%w: PLAN receipt outcomes %v, want %s", harness.ErrStep, values, want)
		}
		return nil
	})
	sc.Step(`^the run "(\w+)" committed PLAN receipts at state versions "([^"]*)"$`, func(ctx context.Context, run, want string) error {
		var got string
		if err := w.Row(ctx, `SELECT coalesce(string_agg(state_version::text,', ' ORDER BY state_version,ordinal),'')
			FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2 AND policy_type='PLAN'`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[run])}, &got); err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%w: PLAN receipt versions %s, want %s", harness.ErrStep, got, want)
		}
		return nil
	})
	sc.Step(`^the answer contains a PLAN error receipt "([A-Z]+)"$`, func(code string) error {
		var run api.Run
		if err := json.Unmarshal(w.Last().Body, &run); err != nil {
			return fmt.Errorf("decode plan error receipt: %w", err)
		}
		for _, receipt := range run.PolicyReceipts {
			if receipt.PolicyType == api.PolicyTypePLAN && receipt.Outcome == "DENY" && receipt.Error != nil && receipt.Error.Code == code {
				return nil
			}
		}
		return fmt.Errorf("%w: no PLAN %s failure receipt", harness.ErrStep, code)
	})
}
