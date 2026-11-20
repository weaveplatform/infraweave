package vcs

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// This timing barrier blocks only the scenario's real queue completion, after
// replacement creation, evidence and cancellation have executed in that same
// transaction. Killing its process must discard all those provisional writes.
func (s *eventSteps) blockPolicyCompletion(ctx context.Context) error {
	db, err := s.comments.w.Querier(ctx)
	if err != nil {
		return err
	}
	_, err = db.Query(ctx, `CREATE FUNCTION accept_block_policy_completion() RETURNS trigger LANGUAGE plpgsql AS $iw$
BEGIN
 IF NEW.kind='vcs.evaluate_event' AND NEW.state='succeeded' THEN PERFORM pg_sleep(10); END IF;
 RETURN NEW;
END $iw$;
CREATE TRIGGER accept_block_policy_completion BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION accept_block_policy_completion();`)
	return err
}

func (s *eventSteps) racePolicyDeliveries(ctx context.Context) error {
	type result struct {
		name, id string
		err      error
	}
	done := make(chan result, 2)
	branch := kernel.NewULID().UUID()
	go func() {
		status, err := s.send(ctx, branch, s.comments.w.IDs["commentBase"], s.comments.w.IDs["seed"], "feature", "valid", "B")
		if err == nil && status != http.StatusAccepted {
			err = fmt.Errorf("%w: raced branch delivery answered %d", harness.ErrStep, status)
		}
		done <- result{"racedBranch", branch, err}
	}()
	go func() {
		err := s.comments.sendRequest(ctx, 12, "open", "seed", "A")
		done <- result{"racedRequest", s.comments.lastDelivery, err}
	}()
	for range 2 {
		r := <-done
		if r.err != nil {
			return r.err
		}
		s.deliveries[r.name] = r.id
	}
	return deps.Eventually(ctx, "both native parents dispatch before child recovery", func(ctx context.Context) (bool, error) {
		var dispatched bool
		err := s.comments.w.Row(
			ctx,
			`SELECT count(*)=2 AND bool_and(state='dispatched') FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id IN ($2,$3)`,
			[]any{harness.UUIDOf(s.comments.fixture.integration), s.deliveries["racedRequest"], s.deliveries["racedBranch"]},
			&dispatched,
		)
		return dispatched, err
	})
}

func (s *eventSteps) crashPolicyCompletion(ctx context.Context) error {
	w := s.comments.w
	if err := deps.Eventually(ctx, "actual child transaction waits at completion", func(ctx context.Context) (bool, error) {
		var blocked bool
		err := w.Row(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event='PgSleep' AND query LIKE '%UPDATE jobs%')`,
			nil,
			&blocked,
		)
		return blocked, err
	}); err != nil {
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
	return deps.Eventually(ctx, "killed child rolls back run evidence and cancellation", func(ctx context.Context) (bool, error) {
		var clean bool
		err := w.Row(
			ctx,
			`SELECT NOT EXISTS(SELECT 1 FROM runs WHERE account_id=$1 AND commit->>'hash'=$2) AND EXISTS(SELECT 1 FROM runs WHERE account_id=$1 AND id=$3 AND state='READY') AND NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='PgSleep')`,
			[]any{harness.UUIDOf(harness.Home.Account), w.IDs["seed"], harness.UUIDOf(w.IDs["prior"])},
			&clean,
		)
		return clean, err
	})
}

func (s *eventSteps) restartPolicyCompletion(ctx context.Context) error {
	w := s.comments.w
	db, err := w.Querier(ctx)
	if err != nil {
		return err
	}
	// Only advance the dead workers' leases. Production claim recovery, event
	// selection and transactional consequences still execute in the new server.
	if _, err := db.Query(
		ctx,
		`DROP TRIGGER accept_block_policy_completion ON jobs; DROP FUNCTION accept_block_policy_completion(); UPDATE jobs SET lease_until=now()-interval '1 second' WHERE kind='vcs.evaluate_event' AND state='running';`,
	); err != nil {
		return err
	}
	for range 2 {
		if _, err := w.StartServer(ctx, nil); err != nil {
			return err
		}
	}
	// Keep A/B pointing to live replacements; retain dead processes for the
	// harness's normal evidence and cleanup bookkeeping.
	w.Servers[0], w.Servers[2] = w.Servers[2], w.Servers[0]
	w.Servers[1], w.Servers[3] = w.Servers[3], w.Servers[1]
	return nil
}

func (s *eventSteps) racedPolicySettled(ctx context.Context) error {
	w := s.comments.w
	var request, branch kernel.ULID
	for name, id := range map[string]*kernel.ULID{"racedRequest": &request, "racedBranch": &branch} {
		if err := w.Row(
			ctx,
			`SELECT id FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id=$2`,
			[]any{harness.UUIDOf(s.comments.fixture.integration), s.deliveries[name]},
			id,
		); err != nil {
			return err
		}
	}
	return deps.Eventually(ctx, "raced recovered policy deliveries commit once", func(ctx context.Context) (bool, error) {
		var done bool
		err := w.Row(
			ctx,
			`SELECT count(*)=2 AND sum(runs_created)=1 AND bool_and(state='dispatched') AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.payload->>'delivery' IN ($4,$5) AND j.kind='vcs.evaluate_event' AND j.state<>'succeeded') FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id IN ($2,$3)`,
			[]any{harness.UUIDOf(s.comments.fixture.integration), s.deliveries["racedRequest"], s.deliveries["racedBranch"], request.String(), branch.String()},
			&done,
		)
		return done, err
	})
}
