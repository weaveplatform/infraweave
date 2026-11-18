package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerPushLimitSteps(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^two infraweave servers, A and B, whose VCS event run ceiling is (\d+)$`, func(ctx context.Context, ceiling int) error {
		w.Settings["vcs"] = map[string]any{"event_runs_ceiling": ceiling}
		return w.TwoReplicas(ctx)
	})
	sc.Step(`^token "(\w+)" sets the VCS event run limit to '([^']*)' with key "([^"]+)" through ([AB])$`,
		func(ctx context.Context, token, limit, key, replica string) error {
			response, err := w.Do(ctx, replica, token, http.MethodPost,
				"/api/v1/account/update-vcs-event-triggered-runs-limit", harness.Body(`{"limit":`+limit+`}`),
				sdk.Header{Name: "Idempotency-Key", Value: key})
			if err != nil && response.Status == 0 {
				return err
			}
			return nil
		})
	sc.Step(`^the VCS event run limit answer is replayed$`, func() error {
		if w.Last().Header.Get("Idempotency-Replayed") != "true" {
			return fmt.Errorf("%w: limit update did not replay", harness.ErrStep)
		}
		return nil
	})
	sc.Step(`^the home account has (\d+) caller-attributed VCS event run limit audit entries$`,
		func(ctx context.Context, want int) error {
			var count int
			var attributed bool
			if err := w.Row(ctx, `SELECT count(*), coalesce(bool_and(actor_username = $2
AND actor_run IS NULL AND coalesce(remote_ip, '') <> '' AND event_type = 'UPDATE'
AND affected_type = 'ACCOUNT' AND affected_id = $3), true)
FROM audit_trail_entries WHERE account_id = $1
AND action = 'account.update_vcs_event_triggered_runs_limit'`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.Home.Key, harness.Home.Account},
				&count, &attributed); err != nil {
				return err
			}
			if count != want || !attributed {
				return fmt.Errorf("%w: %d correctly attributed cap audits=%t, want %d", harness.ErrStep, count, attributed, want)
			}
			return nil
		})
	sc.Step(`^the VCS event run limit error has reason "(minimum|maximum)" and names ceiling (\d+) at /limit$`,
		func(reason string, ceiling int) error {
			var problem struct {
				Errors []struct {
					Pointer string `json:"pointer"`
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"errors"`
			}
			response := w.Last()
			if response.Status != http.StatusUnprocessableEntity || json.Unmarshal(response.Body, &problem) != nil {
				return fmt.Errorf("%w: limit validation returned no 422 field errors", harness.ErrStep)
			}
			for _, field := range problem.Errors {
				if field.Pointer == "/limit" && field.Reason == reason &&
					strings.Contains(field.Message, "ceiling of "+strconv.Itoa(ceiling)) {
					return nil
				}
			}
			return fmt.Errorf("%w: no /limit %s error names ceiling %d", harness.ErrStep, reason, ceiling)
		})
}
