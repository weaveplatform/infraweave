package policies

import (
	"context"
	"fmt"
	"os"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerTriggerRecovery(sc *godog.ScenarioContext, w *harness.World) {
	s := triggerRecovery{w: w}
	sc.Step(`^the trigger fixture pauses queue (claim|completion)$`, s.pause)
	sc.Step(`^both trigger replicas die at the queue boundary for source "(\w+)" without partial fan-out$`, s.crash)
	sc.Step(`^replacement trigger replicas recover the durable job for source "(\w+)"$`, s.restart)
	sc.Step(`^the durable trigger job for source "(\w+)" has completed once$`, s.completed)
}

type triggerRecovery struct {
	w     *harness.World
	phase string
}

func (s *triggerRecovery) pause(ctx context.Context, phase string) error {
	s.phase = phase
	state := "running"
	if phase == "completion" {
		state = "succeeded"
	}
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err
	}
	// The completion barrier runs after provisional child/evidence writes in
	// the production transaction. The claim barrier leaves the terminal
	// source committed while its consumer has not acquired a generation yet.
	_, err = db.Query(ctx, `CREATE FUNCTION accept_pause_trigger() RETURNS trigger LANGUAGE plpgsql AS $iw$
BEGIN
 IF NEW.kind='policy.trigger' AND NEW.state=`+harness.Literal(state)+` AND OLD.state<>NEW.state THEN PERFORM pg_sleep(30); END IF;
 RETURN NEW;
END $iw$;
CREATE TRIGGER accept_pause_trigger BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION accept_pause_trigger();`)
	return err
}

func (s *triggerRecovery) crash(ctx context.Context, source string) error {
	w := s.w
	if err := deps.Eventually(ctx, "real trigger transaction reaches queue barrier", func(ctx context.Context) (bool, error) {
		var paused bool
		err := w.Row(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event='PgSleep' AND query LIKE '%UPDATE jobs%')`,
			nil,
			&paused,
		)
		return paused, err
	}); err != nil {
		return err
	}
	if err := s.noPartial(ctx, source); err != nil {
		return err
	}
	for _, server := range w.Servers[:2] {
		if err := server.Signal(os.Kill); err != nil {
			return err
		}
		if _, err := server.ExitCode(ctx); err != nil {
			return err
		}
	}
	return deps.Eventually(ctx, "dead trigger transaction releases its provisional writes", func(ctx context.Context) (bool, error) {
		var released bool
		err := w.Row(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='PgSleep')`, nil, &released)
		if err == nil && released {
			err = s.noPartial(ctx, source)
		}
		return released, err
	})
}

func (s *triggerRecovery) noPartial(ctx context.Context, source string) error {
	var clean bool
	err := s.w.Row(
		ctx,
		`SELECT
 EXISTS(SELECT 1 FROM runs WHERE account_id=$1 AND id=$2 AND state='CANCELED')
 AND EXISTS(SELECT 1 FROM jobs WHERE account_id=$1 AND kind='policy.trigger' AND payload->>'run'=$3 AND state=$4)
 AND NOT EXISTS(SELECT 1 FROM runs WHERE account_id=$1 AND triggered_by=$5)
 AND NOT EXISTS(SELECT 1 FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2 AND policy_type='TRIGGER')`,
		[]any{
			harness.UUIDOf(harness.Home.Account),
			harness.UUIDOf(s.w.IDs[source]),
			s.w.IDs[source],
			map[string]string{"claim": "pending", "completion": "running"}[s.phase],
			"policy/infra/" + s.w.IDs[source],
		},
		&clean,
	)
	if err != nil {
		return err
	}
	if !clean {
		return fmt.Errorf("%w: source/job lost or provisional fan-out became visible", harness.ErrStep)
	}
	return nil
}

func (s *triggerRecovery) restart(ctx context.Context, source string) error {
	w := s.w
	db, err := w.Querier(ctx)
	if err != nil {
		return err
	}
	if _, err := db.Query(ctx, `DROP TRIGGER accept_pause_trigger ON jobs; DROP FUNCTION accept_pause_trigger();`); err != nil {
		return err
	}
	if _, err := db.Query(
		ctx,
		`UPDATE jobs SET lease_until=now()-interval '1 second' WHERE kind='policy.trigger' AND state='running' AND payload->>'run'=`+harness.Literal(w.IDs[source]),
	); err != nil {
		return err
	}
	for range 2 {
		if _, err := w.StartServer(ctx, nil); err != nil {
			return err
		}
	}
	// Preserve dead processes for normal evidence/cleanup while A/B resolve
	// to the replacements which execute production lease recovery.
	w.Servers[0], w.Servers[2] = w.Servers[2], w.Servers[0]
	w.Servers[1], w.Servers[3] = w.Servers[3], w.Servers[1]
	return nil
}

func (s *triggerRecovery) completed(ctx context.Context, source string) error {
	return deps.Eventually(ctx, "one completed durable terminal trigger", func(ctx context.Context) (bool, error) {
		var complete bool
		err := s.w.Row(
			ctx,
			`SELECT count(*)=1 AND bool_and(state='succeeded') AND bool_and(generation >= $3) FROM jobs WHERE account_id=$1 AND kind='policy.trigger' AND payload->>'run'=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), s.w.IDs[source], map[string]int{"claim": 1, "completion": 2}[s.phase]},
			&complete,
		)
		return complete, err
	})
}
