package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func (s *eventSteps) policyFailureEvidence(ctx context.Context, commit string) error {
	c := s.comments
	var stack kernel.ULID
	if err := c.w.Row(ctx, `SELECT id FROM stacks WHERE account_id=$1 AND slug='infra'`, []any{harness.UUIDOf(harness.Home.Account)}, &stack); err != nil {
		return err
	}
	checks := &checkSteps{
		provider:  c.fixture.provider,
		namespace: c.fixture.namespace,
		github:    c.github,
		gitlab:    c.gitlab,
		cloud:     c.peers.cloud,
		dc:        c.peers.dc,
		azure:     c.peers.azure,
	}
	return deps.Eventually(ctx, "durable failed native check and safe internal error without policy effects", func(ctx context.Context) (bool, error) {
		var durable bool
		err := c.w.Row(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM jobs WHERE account_id=$1 AND kind='vcs.report_check' AND payload#>>'{binding,integration}'=$2 AND payload#>>'{binding,commit}'=$3 AND payload#>>'{binding,push,failed}'='true') AND EXISTS(SELECT 1 FROM events e JOIN stacks s ON s.account_id=e.account_id WHERE e.account_id=$1 AND s.slug='infra' AND e.kind='policy.internal_error' AND e.document->>'type'='GIT_PUSH' AND e.document#>>'{target,id}'=$5) AND EXISTS(SELECT 1 FROM runs WHERE account_id=$1 AND id=$4 AND state='READY') AND NOT EXISTS(SELECT 1 FROM stacks WHERE account_id=$1 AND lock_holder IS NOT NULL)`,
			[]any{harness.UUIDOf(harness.Home.Account), c.fixture.integration, c.w.IDs[commit], harness.UUIDOf(c.w.IDs["prior"]), stack.String()},
			&durable,
		)
		if err != nil || !durable {
			return false, err
		}
		for _, check := range checks.snapshots(false) {
			if check.SHA != c.w.IDs[commit] || check.Name != "infraweave/infra/tracked" {
				continue
			}
			state := strings.ToLower(check.State)
			if state == "failed" || state == "failure" {
				if check.Summary == "" {
					return false, fmt.Errorf("%w: native failed policy check has no summary", harness.ErrStep)
				}
				return true, nil
			}
		}
		return false, nil
	})
}

func (s *eventSteps) policyUnlocked(ctx context.Context) error {
	var answer struct {
		LockedBy *string `json:"lockedBy"`
		LockedAt *int64  `json:"lockedAt"`
		LockNote *string `json:"lockNote"`
	}
	response := s.comments.w.Last()
	if response.Status != http.StatusOK || json.Unmarshal(response.Body, &answer) != nil || answer.LockedBy != nil || answer.LockedAt != nil || answer.LockNote != nil {
		return fmt.Errorf("%w: public stack retains a lock", harness.ErrStep)
	}
	var clear bool
	if err := s.comments.w.Row(
		ctx,
		`SELECT lock_holder IS NULL AND locked_at IS NULL AND locked_by IS NULL AND lock_note IS NULL FROM stacks WHERE account_id=$1 AND slug='infra'`,
		[]any{harness.UUIDOf(harness.Home.Account)},
		&clear,
	); err != nil {
		return err
	}
	if !clear {
		return fmt.Errorf("%w: owned stack lock was not cleared", harness.ErrStep)
	}
	return nil
}
