package policies

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerTask(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the task answer does not deprecate ordinary tasks$`, func() error {
		if w.Last().Header.Get("Deprecation") != "" {
			return fmt.Errorf("%w: an ordinary task was incorrectly deprecated", harness.ErrStep)
		}
		return nil
	})
	sc.Step(`^token "(\w+)" creates and replays one policy-admitted task through both replicas$`, func(ctx context.Context, token string) error {
		header := sdk.Header{Name: sdk.IdempotencyKeyHeader, Value: "one-policy-admitted-task"}
		body := harness.Body(`{"command":"terraform output -json"}`)
		first, err := w.Do(ctx, "A", token, http.MethodPost, "/api/v1/stacks/infra/tasks", body, header)
		if err != nil || first.Status != http.StatusCreated {
			return fmt.Errorf("%w: first policy-admitted task answered %d", harness.ErrStep, first.Status)
		}
		again, err := w.Do(ctx, "B", token, http.MethodPost, "/api/v1/stacks/infra/tasks", body, header)
		if err != nil || again.Status != first.Status || again.Header.Get("Idempotency-Replayed") != "true" ||
			!bytes.Equal(first.Body, again.Body) || first.Header.Get("Deprecation") != again.Header.Get("Deprecation") ||
			first.Header.Get("Link") != again.Header.Get("Link") {
			return fmt.Errorf("%w: admitted task or its deprecation notice was not replayed exactly", harness.ErrStep)
		}
		var tasks, evaluations int
		if err := w.Row(ctx, `SELECT (SELECT count(*) FROM runs WHERE account_id=$1 AND type='TASK'),
			(SELECT count(*) FROM events WHERE account_id=$1 AND kind='policy.evaluated' AND document->>'type'='TASK')`,
			[]any{harness.UUIDOf(harness.Home.Account)}, &tasks, &evaluations); err != nil {
			return err
		}
		if tasks != 1 || evaluations != 1 {
			return fmt.Errorf("%w: TASK replay repeated creation/evaluation: %d/%d", harness.ErrStep, tasks, evaluations)
		}
		return nil
	})
	sc.Step(`^the (TASK|INITIALIZATION) answer carries its deprecation notice$`, func(kind string) error {
		document := "task-policy-verification.md"
		if kind == "INITIALIZATION" {
			document = "initialization-policy-verification.md"
		}
		if w.Last().Header.Get("Deprecation") != "@1795219200" ||
			!strings.Contains(w.Last().Header.Get("Link"), document+`#deprecation-notice>; rel="deprecation"`) || w.Last().Header.Get("Sunset") != "" {
			return fmt.Errorf("%w: %s answer lacks its dated notice/migration link or invents a shutdown deadline", harness.ErrStep, kind)
		}
		return nil
	})
	sc.Step(`^policy "([^"]+)" has (\d+) committed TASK refusals and no task creation effects$`,
		func(ctx context.Context, policy string, expected int) error {
			var tasks, transitions, schedules, assignments, flags, evidence int
			if err := w.Row(ctx, `SELECT
				(SELECT count(*) FROM runs WHERE account_id=$1 AND type='TASK'),
				(SELECT count(*) FROM run_state_transitions t JOIN runs r ON r.account_id=t.account_id AND r.id=t.run_id
					WHERE r.account_id=$1 AND r.type='TASK'),
				(SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='run.schedule'),
				(SELECT count(*) FROM runs WHERE account_id=$1 AND type='TASK' AND worker_id IS NOT NULL),
				(SELECT count(*) FROM run_flags WHERE account_id=$1),
				(SELECT count(*) FROM events WHERE account_id=$1 AND kind='policy.evaluated'
					AND document->>'policy'=$2 AND document->>'type'='TASK' AND document->>'outcome'='DENY'
					AND NOT document ? 'run')`,
				[]any{harness.UUIDOf(harness.Home.Account), w.IDs[policy]}, &tasks, &transitions, &schedules, &assignments, &flags, &evidence); err != nil {
				return err
			}
			if tasks != 0 || transitions != 0 || schedules != 0 || assignments != 0 || flags != 0 || evidence != expected {
				return fmt.Errorf("%w: refused TASK left runs=%d transitions=%d schedules=%d assignments=%d flags=%d evidence=%d, want evidence=%d only",
					harness.ErrStep, tasks, transitions, schedules, assignments, flags, evidence, expected)
			}
			return nil
		})
}
