package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	sourcelab "github.com/weaveplatform/infraweave/internal/vcs/vcslab"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	lab "github.com/weaveplatform/infraweave/test/acceptance/internal/vcslab"
	"github.com/weaveplatform/infraweave/test/deps"
)

func (s *liveLabSteps) registerPolicies(sc *godog.ScenarioContext) {
	sc.Step(`^the live run "(\w+)" retains these policy owners and receipts:$`, s.policyEvidence)
	sc.Step(`^the live stack "([a-z0-9-]+)" has its tracked commit kept as "(\w+)"$`, s.keepTracked)
	sc.Step(`^the live lab pushes an edit and revert with equal endpoint trees$`, s.rangePush)
}

func (s *liveLabSteps) rangePush(ctx context.Context) error {
	if s.native == nil || s.w.Relay == nil {
		return harness.ErrStep
	}
	s.w.Relay.Hold()
	r, err := s.native.PushRoundTrip(ctx, s.w.Dir, "main", "policy-655", lab.GitCredentials{
		User: os.Getenv("SEED_USER"), Token: os.Getenv("SEED_TOKEN"), CAFile: os.Getenv(sourcelab.EnvCAFile),
	})
	if err != nil {
		return err
	}
	s.w.IDs["liveBefore"], s.w.IDs["liveHead"] = r.Before, r.After
	return nil
}

func (s *liveLabSteps) keepTracked(ctx context.Context, stack, name string) error {
	return deps.Eventually(ctx, "live stack's own tracked commit", func(ctx context.Context) (bool, error) {
		r, err := s.w.Do(ctx, "B", "h", http.MethodGet, "/api/v1/stacks/"+url.PathEscape(stack), nil)
		if err != nil {
			return false, err
		}
		var view api.Stack
		if r.Status != http.StatusOK || json.Unmarshal(r.Body, &view) != nil {
			return false, fmt.Errorf("%w: live target stack is unreadable", harness.ErrStep)
		}
		if view.TrackedCommit == nil || len(view.TrackedCommit.Hash) != 40 {
			return false, nil
		}
		s.w.IDs[name] = view.TrackedCommit.Hash
		return true, nil
	})
}

func (s *liveLabSteps) policyEvidence(ctx context.Context, name string, table *godog.Table) error {
	r, err := s.w.Do(ctx, "B", "h", http.MethodGet, "/api/v1/runs/"+s.w.IDs[name], nil)
	if err != nil {
		return err
	}
	var run api.Run
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &run) != nil || run.Flags == nil {
		return fmt.Errorf("%w: live policy run is unreadable", harness.ErrStep)
	}
	flags := []string{}
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != 6 {
			return fmt.Errorf("%w: live policy assertion needs six columns", harness.ErrStep)
		}
		key, policyName, kind := row.Cells[0].Value, row.Cells[1].Value, row.Cells[2].Value
		engine, outcome, flag := row.Cells[3].Value, row.Cells[4].Value, row.Cells[5].Value
		found := false
		for _, receipt := range run.PolicyReceipts {
			if receipt.PolicyName != policyName {
				continue
			}
			if string(receipt.PolicyType) != kind || string(receipt.EngineType) != engine || receipt.Error != nil || receipt.Outcome != outcome ||
				receipt.PolicyVersion == nil || *receipt.PolicyVersion < 1 || receipt.PolicyDeleted || !slices.Contains(receipt.Flags, flag) {
				return fmt.Errorf("%w: live policy lost its committed language, decision or flag", harness.ErrStep)
			}
			found = true
		}
		if !found {
			return fmt.Errorf("%w: live workflow has no committed %s receipt", harness.ErrStep, kind)
		}
		var owners int
		if err := s.w.Row(ctx, `SELECT count(*) FROM run_flags WHERE account_id=$1 AND run_id=$2 AND policy_id=$3 AND flag=$4`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(run.Id), harness.UUIDOf(s.w.IDs[key]), flag}, &owners); err != nil {
			return err
		}
		if owners != 1 {
			return fmt.Errorf("%w: live policy flag has no unique stored owner", harness.ErrStep)
		}
		flags = append(flags, flag)
	}
	slices.Sort(flags)
	flags = slices.Compact(flags)
	var owners int
	if err := s.w.Row(ctx, `SELECT count(*) FROM run_flags WHERE account_id=$1 AND run_id=$2`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(run.Id)}, &owners); err != nil {
		return err
	}
	if owners != len(table.Rows)-1 {
		return fmt.Errorf("%w: live run acquired an unexpected policy flag owner", harness.ErrStep)
	}
	if !slices.Equal(*run.Flags, flags) {
		return fmt.Errorf("%w: live run did not preserve exactly its policy flags", harness.ErrStep)
	}
	return (&pushSteps{w: s.w}).planningEvidence(ctx, name, "B")
}
