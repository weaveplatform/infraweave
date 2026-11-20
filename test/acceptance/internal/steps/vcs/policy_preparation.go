package vcs

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// The database timing barrier holds only this scenario's VCS children. Parent
// processing, API writes and the final evaluator all use their actual services.
func (s *eventSteps) holdPolicyChildren(ctx context.Context) error {
	db, err := s.comments.w.Querier(ctx)
	if err != nil {
		return err
	}
	_, err = db.Query(ctx, `CREATE FUNCTION accept_hold_policy_child() RETURNS trigger LANGUAGE plpgsql AS $iw$
BEGIN
 IF NEW.kind='vcs.evaluate_event' THEN NEW.run_at=now()+interval '1 hour'; END IF;
 RETURN NEW;
END $iw$;
CREATE TRIGGER accept_hold_policy_child BEFORE INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION accept_hold_policy_child();`)
	return err
}

func (s *eventSteps) heldPolicyChild(ctx context.Context, name string) error {
	w := s.comments.w
	return deps.Eventually(ctx, "parent dispatched and native-less child held", func(ctx context.Context) (bool, error) {
		var delivery kernel.ULID
		var dispatched, held bool
		if err := w.Row(
			ctx,
			`SELECT id,state='dispatched' FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2 AND delivery_id=$3`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.comments.fixture.integration), s.deliveries[name]},
			&delivery,
			&dispatched,
		); err != nil {
			return false, err
		}
		if !dispatched {
			return false, nil
		}
		err := w.Row(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='vcs.evaluate_event' AND j.payload->>'delivery'=$1 AND j.state='pending' AND j.run_at>now()+interval '30 minutes') AND EXISTS(SELECT 1 FROM vcs_prepared_events p WHERE p.delivery_id=$2 AND (p.event->'request' IS NULL OR p.event->'request'='null'::jsonb))`,
			[]any{delivery.String(), harness.UUIDOf(delivery.String())},
			&held,
		)
		return held, err
	})
}

func (s *eventSteps) releasePolicyChildren(ctx context.Context) error {
	db, err := s.comments.w.Querier(ctx)
	if err != nil {
		return err
	}
	_, err = db.Query(
		ctx,
		`DROP TRIGGER accept_hold_policy_child ON jobs; DROP FUNCTION accept_hold_policy_child(); UPDATE jobs SET run_at=now() WHERE kind='vcs.evaluate_event' AND state='pending';`,
	)
	return err
}

func (s *eventSteps) failedPolicyCheck(ctx context.Context, name string) error {
	w := s.comments.w
	var delivery kernel.ULID
	if err := w.Row(
		ctx,
		`SELECT id FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2 AND delivery_id=$3`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.comments.fixture.integration), s.deliveries[name]},
		&delivery,
	); err != nil {
		return err
	}
	var queued bool
	if err := deps.Eventually(ctx, "failed policy check committed or evaluation failed", func(ctx context.Context) (bool, error) {
		var dead bool
		err := w.Row(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='vcs.report_check' AND j.account_id=$1 AND j.payload#>>'{binding,push,failed}'='true' AND j.payload#>>'{binding,integration}'=$2 AND j.payload#>>'{binding,commit}'=$3),EXISTS(SELECT 1 FROM jobs j WHERE j.kind='vcs.evaluate_event' AND j.account_id=$1 AND j.payload->>'delivery'=$4 AND j.state='dead')`,
			[]any{harness.UUIDOf(harness.Home.Account), s.comments.fixture.integration, w.IDs["seed"], delivery.String()},
			&queued,
			&dead,
		)
		return queued || dead, err
	}); err != nil {
		return err
	}
	if !queued {
		return fmt.Errorf("%w: policy evaluation died without committing its failed native check", harness.ErrStep)
	}
	return nil
}
