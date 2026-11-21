package policies

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerInitialization(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^run "([^"]+)" has (\d+) INITIALIZATION decisions and (no|one) worker preparation$`,
		func(ctx context.Context, name string, decisions int, preparation string) error {
			var receipts, events, prepared int
			var assigned bool
			if err := w.Row(ctx, `SELECT
				(SELECT count(*) FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2 AND policy_type='INITIALIZATION'),
				(SELECT count(*) FROM events WHERE account_id=$1 AND kind='policy.evaluated' AND document->>'run'=$3 AND document->>'type'='INITIALIZATION'),
				(SELECT count(*) FROM run_state_transitions WHERE account_id=$1 AND run_id=$2 AND state='PREPARING'),
				worker_id IS NOT NULL FROM runs WHERE account_id=$1 AND id=$2`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[name]), w.IDs[name]}, &receipts, &events, &prepared, &assigned); err != nil {
				return err
			}
			want := 0
			if preparation == "one" {
				want = 1
			}
			if receipts != decisions || events != decisions || prepared != want || assigned != (want == 1) {
				return fmt.Errorf("%w: initialization effects: receipts=%d events=%d preparations=%d assigned=%t", harness.ErrStep, receipts, events, prepared, assigned)
			}
			return nil
		})
	sc.Step(`^the INITIALIZATION decisions of run "([^"]+)" retain "([^"]+)"$`,
		func(ctx context.Context, name, want string) error {
			var messages []string
			if err := w.Row(ctx, `SELECT array_agg(message ORDER BY message)
				FROM run_policy_receipts r CROSS JOIN LATERAL jsonb_array_elements_text(r.decision->'deny') AS message
				WHERE account_id=$1 AND run_id=$2 AND policy_type='INITIALIZATION'`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[name])}, &messages); err != nil {
				return err
			}
			if strings.Join(messages, ", ") != want {
				return fmt.Errorf("%w: stored initialization messages %v want %s", harness.ErrStep, messages, want)
			}
			return nil
		})
	sc.Step(`^two duplicate scheduler jobs are enqueued for run "([^"]+)"$`, func(ctx context.Context, name string) error {
		var account, stack kernel.ULID
		if err := w.Row(ctx, `SELECT account_id,stack_id FROM runs WHERE account_id=$1 AND id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[name])}, &account, &stack); err != nil {
			return err
		}
		db, err := w.Database(ctx)
		if err != nil {
			return err
		}
		conn, err := pgx.Connect(ctx, db.DSN)
		if err != nil {
			return fmt.Errorf("connect scheduler replay fixture: %w", err)
		}
		defer conn.Close(context.WithoutCancel(ctx))
		jobs := queue.New(conn)
		kind := queue.Kind{Name: "run.schedule", Queue: queue.Default, Policy: queue.Coalesce}
		for range 2 {
			if _, err := jobs.Enqueue(
				ctx,
				kind,
				map[string]string{"stack": stack.String()},
				queue.Account(account),
				queue.UniqueKey("init-replay:"+kernel.NewULID().String()),
			); err != nil {
				return fmt.Errorf("enqueue duplicate scheduler job: %w", err)
			}
		}
		return nil
	})
}
