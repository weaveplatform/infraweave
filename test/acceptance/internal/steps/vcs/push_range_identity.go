package vcs

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func (s *eventSteps) registerRangeIdentity(sc *godog.ScenarioContext) {
	sc.Step(`^the event fixture holds future source preparations$`, s.holdRangePreparation)
	sc.Step(`^the event push carries a "(different|missing)" signed repository identity$`, func(mode string) { s.nativeIdentity = mode })
	sc.Step(`^event delivery "(\w+)" becomes due for one real preparation attempt$`, s.dueRangeIdentity)
	sc.Step(`^event delivery "(\w+)" refuses its repository identity before source reads or effects$`, s.refusedRangeIdentity)
}

func (s *eventSteps) decorateNativeIdentity(body map[string]any) {
	if s.nativeIdentity == "" {
		return
	}
	root := body
	if s.comments.fixture.provider == "Azure DevOps" {
		root = body["resource"].(map[string]any) //nolint:forcetypeassert // native fake envelope
	}
	key := "repository"
	if s.comments.fixture.provider == "GitLab" {
		key = "project"
	}
	repo := root[key].(map[string]any) //nolint:forcetypeassert // native fake repository
	field := "id"
	if s.comments.fixture.provider == "Bitbucket Cloud" {
		field = "uuid"
	}
	if s.nativeIdentity == "missing" {
		delete(repo, field)
		return
	}
	switch s.comments.fixture.provider {
	case "Bitbucket Cloud", "Azure DevOps":
		repo[field] = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	default:
		repo[field] = 987654321
	}
}

// Holding the inserted parent lets the fixture set its attempt budget before
// any real consumer claims it. Neither handler nor queue completion is mocked.
func (s *eventSteps) holdRangePreparation(ctx context.Context) error {
	db, err := s.comments.w.Querier(ctx)
	if err != nil {
		return err
	}
	_, err = db.Query(ctx, `CREATE FUNCTION accept_hold_range_parent() RETURNS trigger LANGUAGE plpgsql AS $iw$
BEGIN
 IF NEW.kind='vcs.process_delivery' THEN NEW.run_at=now()+interval '1 hour'; END IF;
 RETURN NEW;
END $iw$;
CREATE TRIGGER accept_hold_range_parent BEFORE INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION accept_hold_range_parent();`)
	return err
}

func (s *eventSteps) sourceReads() int {
	c := s.comments
	switch c.fixture.provider {
	case "GitHub":
		return c.github.fake.SourceReads()
	case "GitLab":
		return c.gitlab.fake.SourceReads()
	case "Bitbucket Cloud":
		return c.peers.cloud.fake.SourceReads()
	case "Bitbucket DC":
		return c.peers.dc.fake.SourceReads()
	case "Azure DevOps":
		return c.peers.azure.fake.SourceReads()
	}
	return -1
}

func (s *eventSteps) dueRangeIdentity(ctx context.Context, name string) error {
	id, err := s.rangeDelivery(ctx, name)
	if err != nil {
		return err
	}
	s.sourceReadCount = s.sourceReads()
	var due bool
	err = s.comments.w.Row(ctx, `WITH changed AS (UPDATE jobs SET run_at=now(),max_attempts=1
 WHERE account_id=$1 AND kind='vcs.process_delivery' AND payload->>'id'=$2 AND state='pending' AND attempt=0
 RETURNING 1) SELECT count(*)=1 FROM changed`,
		[]any{harness.UUIDOf(harness.Home.Account), id.String()}, &due)
	if err == nil && !due {
		return fmt.Errorf("%w: identity fixture parent was not held before its first claim", harness.ErrStep)
	}
	return err
}

func (s *eventSteps) refusedRangeIdentity(ctx context.Context, name string) error {
	id, err := s.rangeDelivery(ctx, name)
	if err != nil {
		return err
	}
	if err := deps.Eventually(ctx, "identity refusal before source reads or effects", func(ctx context.Context) (bool, error) {
		if s.sourceReads() != s.sourceReadCount {
			return false, fmt.Errorf("%w: mismatched or missing identity reached native source objects/history", deps.ErrPermanent)
		}
		var safe, failed bool
		err := s.comments.w.Row(ctx, `SELECT state='failed',runs_created=0 AND last_error IN
  ('vcs: webhook payload could not be parsed','vcs: delivery preparation failed')
  AND NOT EXISTS(SELECT 1 FROM vcs_prepared_events p WHERE p.account_id=d.account_id AND p.delivery_id=d.id)
  AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.account_id=d.account_id AND j.kind='vcs.evaluate_event' AND j.payload->>'delivery'=$3)
  AND EXISTS(SELECT 1 FROM jobs j WHERE j.account_id=d.account_id AND j.kind='vcs.process_delivery' AND j.payload->>'id'=$3 AND j.state='dead' AND j.attempt=1)
  FROM vcs_deliveries d WHERE account_id=$1 AND id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), id.UUID(), id.String()}, &failed, &safe)
		if err != nil || !failed {
			return false, err
		}
		if !safe {
			return false, fmt.Errorf("%w: identity refusal committed preparation or unsafe failure metadata", harness.ErrStep)
		}
		return true, nil
	}); err != nil {
		return err
	}
	if s.sourceReads() != s.sourceReadCount {
		return fmt.Errorf("%w: mismatched or missing identity reached native source objects/history", harness.ErrStep)
	}
	return nil
}
