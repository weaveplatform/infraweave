package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

const accountReplayBody = `{"name":"Account replay fixture","type":"PLAN","body":"package infraweave\n"}`

func registerAccountReplay(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^token "(\w+)" retries the account replay request using key "([^"]+)" through ([AB])$`, func(ctx context.Context, token, key, replica string) error {
		_, err := w.Do(ctx, replica, token, http.MethodPost, "/api/v1/policies", json.RawMessage(accountReplayBody), sdk.Header{Name: sdk.IdempotencyKeyHeader, Value: key})
		return err
	})
	registerReplayDeprecation(sc, w)

	sc.Step(
		`^token "(\w+)" (creates|replays) its policy "([^"]+)" in the (home|sibling) account using key "([^"]+)" through ([AB])$`,
		func(ctx context.Context, token, mode, name, who, key, replica string) error {
			response, err := w.Do(
				ctx,
				replica,
				token,
				http.MethodPost,
				"/api/v1/policies",
				json.RawMessage(accountReplayBody),
				sdk.Header{Name: sdk.IdempotencyKeyHeader, Value: key},
			)
			if err != nil {
				return err
			}
			var policy api.Policy
			if response.Status != http.StatusCreated || json.Unmarshal(response.Body, &policy) != nil {
				return fmt.Errorf("%w: account %s policy request returned %d", harness.ErrStep, who, response.Status)
			}
			tenant := harness.Tenants[who]
			if policy.Space != tenant.Slug {
				return fmt.Errorf("%w: %s-context request recovered a policy from space %s", harness.ErrStep, who, policy.Space)
			}
			if mode == "creates" {
				if response.Header.Get("Idempotency-Replayed") == "true" {
					return fmt.Errorf("%w: account %s reused a different account/actor replay", harness.ErrStep, who)
				}
				w.IDs[name] = policy.Id
				count, _ := strconv.Atoi(w.IDs["account-replay-count/"+who])
				w.IDs["account-replay-count/"+who] = strconv.Itoa(count + 1)
			} else if policy.Id != w.IDs[name] || response.Header.Get("Idempotency-Replayed") != "true" {
				return fmt.Errorf("%w: same account/actor did not replay its original policy", harness.ErrStep)
			}
			var owned, total int
			if err := w.Row(
				ctx,
				`SELECT count(*) FILTER (WHERE id=$2),count(*) FROM policies WHERE account_id=$1 AND name='Account replay fixture'`,
				[]any{harness.UUIDOf(tenant.Account), harness.UUIDOf(policy.Id)},
				&owned,
				&total,
			); err != nil {
				return err
			}
			expected, _ := strconv.Atoi(w.IDs["account-replay-count/"+who])
			if owned != 1 || total != expected {
				return fmt.Errorf("%w: account %s policy ownership/count is %d/%d, want 1/%d", harness.ErrStep, who, owned, total, expected)
			}
			return nil
		},
	)
}
