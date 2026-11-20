package vcs

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func (s *eventSteps) refuseRangeSource(mode string) {
	s.rangeFault = mode
	c, fault := s.comments, mode == "rate limit"
	switch c.fixture.provider {
	case "GitHub":
		kind := githubfake.ServerError
		if fault {
			kind = githubfake.TooManyRequests
		}
		c.github.fake.FailPath("/repos/"+c.fixture.namespace+"/infra/compare/", kind, time.Hour)
	case "GitLab":
		kind := gitlabfake.ServerError
		if fault {
			kind = gitlabfake.TooManyRequests
		}
		c.gitlab.fake.FailPath("/projects/"+c.fixture.namespace+"/infra/repository/tree", kind, time.Hour)
	case "Bitbucket Cloud":
		kind := bitbucketcloudfake.ServerError
		if fault {
			kind = bitbucketcloudfake.TooManyRequests
		}
		c.peers.cloud.fake.FailPath("/repositories/"+c.fixture.namespace+"/infra/commits", kind, time.Hour)
	case "Bitbucket DC":
		kind := bitbucketdcfake.ServerError
		if fault {
			kind = bitbucketdcfake.TooManyRequests
		}
		c.peers.dc.fake.FailPath("/projects/"+c.fixture.namespace+"/repos/infra/files", kind, time.Hour)
	case "Azure DevOps":
		kind := azuredevopsfake.ServerError
		if fault {
			kind = azuredevopsfake.TooManyRequests
		}
		c.peers.azure.fake.FailPath("/"+c.fixture.namespace+"/_apis/git/repositories/infra/commits/"+c.w.IDs["collected"]+"/changes", kind, time.Hour)
	}
}

func (s *eventSteps) rangeDelivery(ctx context.Context, name string) (kernel.ULID, error) {
	var id kernel.ULID
	err := s.comments.w.Row(ctx, `SELECT id FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2 AND delivery_id=$3`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.comments.fixture.integration), s.deliveries[name]}, &id)
	return id, err
}

func (s *eventSteps) rangeWaiting(ctx context.Context, name string) error {
	id, err := s.rangeDelivery(ctx, name)
	if err != nil {
		return err
	}
	return deps.Eventually(ctx, "range source refusal without provisional effects", func(ctx context.Context) (bool, error) {
		var safe, waiting bool
		err := s.comments.w.Row(ctx, `SELECT d.state='received' AND d.runs_created=0
		 AND NOT EXISTS(SELECT 1 FROM vcs_prepared_events p WHERE p.account_id=d.account_id AND p.delivery_id=d.id)
		 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.account_id=d.account_id AND j.kind='vcs.evaluate_event' AND j.payload->>'delivery'=$3),
		 EXISTS(SELECT 1 FROM jobs j WHERE j.account_id=d.account_id AND j.kind='vcs.process_delivery' AND j.payload->>'id'=$3
		 AND j.state='pending' AND j.run_at>now() AND ((j.attempt=0 AND $4='rate limit' AND coalesce(j.last_error,'')='')
		 OR (j.attempt>0 AND $4='server failure' AND j.last_error='vcs: delivery preparation failed')))
		 FROM vcs_deliveries d WHERE d.account_id=$1 AND d.id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), id.UUID(), id.String(), s.rangeFault}, &safe, &waiting)
		return safe && waiting, err
	})
}

func (s *eventSteps) clearRangeSource() {
	c := s.comments
	switch c.fixture.provider {
	case "GitHub":
		c.github.fake.Fail(githubfake.None, 0)
	case "GitLab":
		c.gitlab.fake.Fail(gitlabfake.None, 0)
	case "Bitbucket Cloud":
		c.peers.cloud.fake.Fail(bitbucketcloudfake.None, 0)
	case "Bitbucket DC":
		c.peers.dc.fake.Fail(bitbucketdcfake.None, 0)
	case "Azure DevOps":
		c.peers.azure.fake.Fail(azuredevopsfake.None, 0)
	}
}

func (s *eventSteps) resumeRangeSource(ctx context.Context, name string) error {
	id, err := s.rangeDelivery(ctx, name)
	if err != nil {
		return err
	}
	s.clearRangeSource()
	var due bool
	// Only the selected fixture job's scheduling time changes. The real queue
	// still claims, prepares, fences and commits the entire retry itself.
	err = s.comments.w.Row(ctx, `WITH changed AS (UPDATE jobs SET run_at=now() WHERE account_id=$1 AND kind='vcs.process_delivery'
	 AND payload->>'id'=$2 AND state='pending' RETURNING 1) SELECT count(*)=1 FROM changed`,
		[]any{harness.UUIDOf(harness.Home.Account), id.String()}, &due)
	if err == nil && !due {
		return fmt.Errorf("%w: range retry was not pending", harness.ErrStep)
	}
	return err
}

func (s *eventSteps) expireRangeSource(ctx context.Context, name string) error {
	id, err := s.rangeDelivery(ctx, name)
	if err != nil {
		return err
	}
	var expired bool
	err = s.comments.w.Row(ctx, `WITH changed AS (UPDATE vcs_deliveries SET deadline=now()-interval '1 second'
	 WHERE account_id=$1 AND id=$2 AND state='received' RETURNING 1) SELECT count(*)=1 FROM changed`,
		[]any{harness.UUIDOf(harness.Home.Account), id.UUID()}, &expired)
	if err != nil {
		return err
	}
	if !expired {
		return fmt.Errorf("%w: range source delivery was not waiting", harness.ErrStep)
	}
	if err := s.resumeRangeSource(ctx, name); err != nil {
		return err
	}
	return deps.Eventually(ctx, "expired range source delivery without effects", func(ctx context.Context) (bool, error) {
		var safe bool
		err := s.comments.w.Row(ctx, `SELECT state='expired' AND runs_created=0
		 AND NOT EXISTS(SELECT 1 FROM vcs_prepared_events p WHERE p.account_id=d.account_id AND p.delivery_id=d.id)
		 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.account_id=d.account_id AND j.kind='vcs.evaluate_event' AND j.payload->>'delivery'=$3)
		 FROM vcs_deliveries d WHERE account_id=$1 AND id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), id.UUID(), id.String()}, &safe)
		return safe, err
	})
}
