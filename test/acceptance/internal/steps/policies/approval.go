package policies

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"
	"golang.org/x/sync/errgroup"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerApproval(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the answer contains an APPROVAL error receipt$`, func() error {
		var run api.Run
		if err := json.Unmarshal(w.Last().Body, &run); err != nil {
			return fmt.Errorf("decode run approval receipts: %w", err)
		}
		for _, receipt := range run.PolicyReceipts {
			if receipt.PolicyType == api.PolicyTypeAPPROVAL && receipt.Outcome == "UNDECIDED" && receipt.Error != nil && receipt.Error.Code != "" {
				return nil
			}
		}
		return fmt.Errorf("%w: no safe approval error receipt", harness.ErrStep)
	})
	sc.Step(`^token "(\w+)" reviews run "([^"]+)" once and replays the same key through the other server$`, func(ctx context.Context, token, run string) error {
		path := "/api/v1/stacks/infra/runs/review"
		body := harness.Body(w.Resolve(`{"run":"{` + run + `}","decision":"APPROVE","note":"One immutable request","stateVersion":1}`))
		header := sdk.Header{Name: sdk.IdempotencyKeyHeader, Value: "approval-one-request"}
		first, err := w.Do(ctx, "A", token, http.MethodPost, path, body, header)
		if err != nil || first.Status != http.StatusOK {
			return fmt.Errorf("%w: first review status %d", harness.ErrStep, first.Status)
		}
		if err := deps.Eventually(ctx, "reviewed run READY", func(ctx context.Context) (bool, error) {
			var state string
			if err := w.Row(
				ctx,
				`SELECT state FROM runs WHERE account_id=$1 AND id=$2`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[run])},
				&state,
			); err != nil {
				return false, err
			}
			return state == "READY", nil
		}); err != nil {
			return err
		}
		counts := func() ([3]int, error) {
			var out [3]int
			err := w.Row(ctx, `SELECT (SELECT count(*) FROM run_reviews WHERE account_id=$1 AND run_id=$2),
				(SELECT count(*) FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2),
				(SELECT count(*) FROM run_state_transitions WHERE account_id=$1 AND run_id=$2)`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[run])}, &out[0], &out[1], &out[2])
			return out, err
		}
		before, err := counts()
		if err != nil {
			return err
		}
		again, err := w.Do(ctx, "B", token, http.MethodPost, path, body, header)
		if err != nil || again.Status != first.Status || again.Header.Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body, again.Body) {
			return fmt.Errorf("%w: review was not replayed byte-for-byte", harness.ErrStep)
		}
		after, err := counts()
		if err != nil {
			return err
		}
		if after != before || after[0] != 1 || after[2] != 2 {
			return fmt.Errorf("%w: review replay changed persisted counts: %v -> %v", harness.ErrStep, before, after)
		}
		return nil
	})
	sc.Step(`^tokens "(\w+)" and "(\w+)" concurrently approve run "([^"]+)" through different servers$`, func(ctx context.Context, first, second, run string) error {
		names := []string{first, second}
		clients := make([]*sdk.Client, 2)
		for i, name := range names {
			replica := "A"
			if i == 1 {
				replica = "B"
			}
			c, err := w.Client(replica, w.Tokens[name])
			if err != nil {
				return err
			}
			clients[i] = c
		}
		g, gctx := errgroup.WithContext(ctx)
		body := harness.Body(w.Resolve(`{"run":"{` + run + `}","decision":"APPROVE","stateVersion":1}`))
		for i, c := range clients {
			g.Go(func() error {
				response, err := c.Do(
					gctx,
					http.MethodPost,
					"/api/v1/stacks/infra/runs/review",
					body,
					sdk.Header{Name: sdk.IdempotencyKeyHeader, Value: fmt.Sprintf("concurrent-review-%d", i)},
				)
				if err != nil || response == nil || response.Status != http.StatusOK {
					return fmt.Errorf("%w: concurrent reviewer %d failed", harness.ErrStep, i)
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}
		var reviews, authors, ready int
		if err := w.Row(ctx, `SELECT count(*),count(DISTINCT author_key),
			(SELECT count(*) FROM run_state_transitions WHERE account_id=$1 AND run_id=$2 AND state='READY')
			FROM run_reviews WHERE account_id=$1 AND run_id=$2`, []any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[run])}, &reviews, &authors, &ready); err != nil {
			return err
		}
		if reviews != 2 || authors != 2 || ready != 1 {
			return fmt.Errorf("%w: concurrent reviews=%d authors=%d READY transitions=%d", harness.ErrStep, reviews, authors, ready)
		}
		return nil
	})
}
