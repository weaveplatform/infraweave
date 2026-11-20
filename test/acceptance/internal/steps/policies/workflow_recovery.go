package policies

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerWorkflowRecovery(sc *godog.ScenarioContext, w *harness.World) {
	s := workflowRecovery{w: w}
	sc.Step(`^the workflow fixture pauses the native PLAN projection$`, s.pause)
	sc.Step(`^the actual PLAN report for run "(\w+)" waits without committed policy effects$`, s.observe)
	sc.Step(`^the workflow fixture aborts that report transaction and releases the real worker retry$`, s.abort)
}

type workflowRecovery struct {
	w   *harness.World
	pid int
}

func (s *workflowRecovery) pause(ctx context.Context) error {
	// This event is appended after provisional state, receipts and owner flags
	// in the real report transaction. No worker response or policy is faked.
	_, err := s.w.Query(ctx, `CREATE FUNCTION accept_hold_workflow_projection() RETURNS trigger LANGUAGE plpgsql AS $iw$
BEGIN
 IF NEW.kind='run_updated' AND NEW.document->>'state'='UNCONFIRMED'
    AND NEW.document->'policy_receipts' @> '[{"type":"PLAN"}]'::jsonb THEN
  PERFORM pg_sleep(45);
 END IF;
 RETURN NEW;
END $iw$;
CREATE TRIGGER accept_hold_workflow_projection BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION accept_hold_workflow_projection();`)
	return err
}

func (s *workflowRecovery) observe(ctx context.Context, run string) error {
	if err := deps.Eventually(ctx, "actual worker report pauses before its event commit", func(ctx context.Context) (bool, error) {
		err := s.w.Row(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database()
 AND pid<>pg_backend_pid() AND wait_event='PgSleep' AND query ILIKE '%insert into events%' LIMIT 1`, nil, &s.pid)
		return err == nil && s.pid > 0, err
	}); err != nil {
		return err
	}
	var clean bool
	if err := s.w.Row(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2)
 AND NOT EXISTS(SELECT 1 FROM run_flags WHERE account_id=$1 AND run_id=$2)
 AND NOT EXISTS(SELECT 1 FROM events WHERE account_id=$1 AND kind='policy_evaluated')
 AND NOT EXISTS(SELECT 1 FROM events WHERE account_id=$1 AND subject_kind='run' AND subject_id=$3 AND sequence>5)`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.w.IDs[run]), s.w.IDs[run]}, &clean); err != nil {
		return err
	}
	if !clean {
		return fmt.Errorf("%w: provisional worker policy effects escaped the report transaction", harness.ErrStep)
	}
	return workflowFanoutReferences(ctx, s.w)
}

func workflowFanoutReferences(ctx context.Context, w *harness.World) error {
	var raw []byte
	if err := w.Row(ctx, `SELECT coalesce(jsonb_agg(payload),'[]'::jsonb) FROM jobs WHERE account_id=$1 AND kind='events.fanout'`,
		[]any{harness.UUIDOf(harness.Home.Account)}, &raw); err != nil {
		return err
	}
	var refs []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &refs); err != nil {
		return fmt.Errorf("decode workflow event references: %w", err)
	}
	for _, ref := range refs {
		if _, err := kernel.ParseULID(ref.ID); err != nil {
			return fmt.Errorf("decode workflow event identity: %w", err)
		}
		var exists bool
		if err := w.Row(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE account_id=$1 AND id=$2)`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(ref.ID)}, &exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: workflow fan-out job escaped its event transaction", harness.ErrStep)
		}
	}
	return nil
}

func (s *workflowRecovery) abort(ctx context.Context) error {
	if s.pid <= 0 {
		return fmt.Errorf("%w: no observed workflow transaction to abort", harness.ErrStep)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.w.Query(ctx, `DROP TRIGGER accept_hold_workflow_projection ON events; DROP FUNCTION accept_hold_workflow_projection();`)
		done <- err
	}()
	// Queue the DDL before dropping the backend: fresh report retries must
	// wait behind it rather than entering the same pause a second time.
	if err := deps.Eventually(ctx, "workflow pause removal waits behind the real report", func(ctx context.Context) (bool, error) {
		var waiting bool
		err := s.w.Row(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
 AND wait_event_type='Lock' AND query LIKE 'DROP TRIGGER accept_hold_workflow_projection%')`, nil, &waiting)
		return waiting, err
	}); err != nil {
		return err
	}
	var killed bool
	if err := s.w.Row(ctx, `SELECT pg_terminate_backend($1)`, []any{s.pid}, &killed); err != nil {
		return err
	}
	if !killed {
		return fmt.Errorf("%w: actual worker report backend was not terminated", harness.ErrStep)
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
