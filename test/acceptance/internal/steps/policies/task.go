package policies

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerTask(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the TASK answer carries its deprecation notice$`, func() error {
		if w.Last().Header.Get("Deprecation") == "" {
			return fmt.Errorf("%w: TASK answer has no Deprecation header", harness.ErrStep)
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
