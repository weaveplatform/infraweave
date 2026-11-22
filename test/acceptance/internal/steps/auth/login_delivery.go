package auth

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (b *browserSteps) registerLoginDelivery(sc *godog.ScenarioContext) {
	snapshot := func(ctx context.Context) (string, error) {
		var metadata string
		err := b.w.Row(ctx, `SELECT jsonb_build_object(
   'sessions',(SELECT jsonb_agg(jsonb_build_array(id,admin,teams,revoked_at) ORDER BY id) FROM sessions WHERE account_id=$1 AND actor_id=$2),
   'grants',(SELECT jsonb_agg(jsonb_build_array(g.id,g.session_id,g.space_id,g.role_id,g.origin) ORDER BY g.id) FROM session_grants g JOIN sessions s ON s.account_id=g.account_id AND s.id=g.session_id WHERE s.account_id=$1 AND s.actor_id=$2))::text`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(harness.Member.Key)}, &metadata)
		return metadata, err
	}
	sc.Step(`^the LOGIN caller's permission rows are recorded as "([^"]+)"$`, func(ctx context.Context, name string) error {
		value, err := snapshot(ctx)
		if err == nil {
			b.w.IDs["login-permission/"+name] = value
		}
		return err
	})
	sc.Step(`^the LOGIN caller's permission rows still match "([^"]+)"$`, func(ctx context.Context, name string) error {
		value, err := snapshot(ctx)
		if err != nil {
			return err
		}
		before, ok := b.w.IDs["login-permission/"+name]
		if !ok || value != before {
			return fmt.Errorf("%w: refused LOGIN changed actor sessions or grants", harness.ErrStep)
		}
		return nil
	})
	sc.Step(`^policy "([^"]+)" has (\d+) committed LOGIN evaluations without run effects$`, func(ctx context.Context, name string, want int) error {
		var evaluations, runs, flags, receipts, schedules int
		var noRun bool
		if err := b.w.Row(ctx, `SELECT count(*),coalesce(bool_and(run_id IS NULL),false),
    (SELECT count(*) FROM runs WHERE account_id=$1),
    (SELECT count(*) FROM run_flags WHERE account_id=$1),
    (SELECT count(*) FROM run_policy_receipts WHERE account_id=$1),
    (SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='run.schedule')
    FROM policy_evaluations WHERE account_id=$1 AND policy_id=$2 AND type='LOGIN'`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(b.w.IDs[name])}, &evaluations, &noRun, &runs, &flags, &receipts, &schedules); err != nil {
			return err
		}
		if evaluations != want || !noRun || runs != 0 || flags != 0 || receipts != 0 || schedules != 0 {
			return fmt.Errorf(
				"%w: LOGIN effects evaluations=%d noRun=%t runs=%d flags=%d receipts=%d schedules=%d",
				harness.ErrStep,
				evaluations,
				noRun,
				runs,
				flags,
				receipts,
				schedules,
			)
		}
		// Reading metadata here is separate from a private sample read; row-bound
		// sealed inputs and decision/print documents are deliberately not selected.
		return nil
	})
}
