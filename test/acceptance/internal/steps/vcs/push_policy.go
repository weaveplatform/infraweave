package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func (s *pushSteps) policyCancellation(ctx context.Context, prior, replacement, state, replica string) error {
	r, err := s.w.Do(ctx, replica, "h", http.MethodGet, "/api/v1/runs/"+s.w.IDs[prior], nil)
	if err != nil {
		return err
	}
	var run api.Run
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &run) != nil || string(run.State) != state || len(run.History) == 0 {
		return fmt.Errorf("%w: push cancellation did not commit terminal run state %s", harness.ErrStep, state)
	}
	note := "canceled by push policy in favor of run " + s.w.IDs[replacement]
	last := run.History[len(run.History)-1]
	if string(last.State) != state || last.Note == nil || *last.Note != note {
		return fmt.Errorf("%w: push cancellation lost the exact replacement note", harness.ErrStep)
	}
	count := 0
	for _, transition := range run.History {
		if transition.Note != nil && *transition.Note == note {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("%w: push cancellation was replayed %d times", harness.ErrStep, count)
	}
	return nil
}

func (s *pushSteps) pushReceipt(policy string) error {
	var run api.Run
	if json.Unmarshal(s.w.Last().Body, &run) != nil || len(run.PolicyReceipts) != 1 {
		return fmt.Errorf("%w: expected one committed push receipt", harness.ErrStep)
	}
	r := run.PolicyReceipts[0]
	version, err := strconv.ParseInt(s.w.IDs[policy+"Version"], 10, 64)
	if err != nil {
		return fmt.Errorf("%w: no expected policy version", harness.ErrStep)
	}
	if r.PolicySlug == nil || *r.PolicySlug != s.w.IDs[policy] || r.PolicyType != api.PolicyTypeGITPUSH ||
		r.Outcome != "PUSH" || r.PolicyVersion == nil || *r.PolicyVersion != version || r.CreatedAt <= 0 || r.Error != nil ||
		r.PolicyDeleted || r.Sampled || !slices.Equal(r.Flags, []string{"pushed"}) {
		return fmt.Errorf("%w: initial public push receipt lost policy/version/outcome/flags", harness.ErrStep)
	}
	return nil
}

func (s *pushSteps) policyCheckJob(ctx context.Context, commit string) error {
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err
	}
	sha, err := s.commitHash(commit)
	if err != nil {
		return err
	}
	raw, err := db.Query(ctx, `SELECT count(*) FROM jobs WHERE kind='vcs.report_check' AND payload#>>'{binding,commit}'=`+harness.Literal(sha))
	if err != nil {
		return err
	}
	count, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || count < 1 {
		return fmt.Errorf("%w: ignored policy decision left no durable provider check job", harness.ErrStep)
	}
	return nil
}

func (s *pushSteps) requestPolicyJobs(ctx context.Context, want int) error {
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err
	}
	raw, err := db.Query(ctx, `SELECT count(*) FROM jobs WHERE kind='vcs.evaluate_event' AND account_id=`+harness.Literal(harness.UUIDOf(harness.Home.Account))+`::uuid`)
	if err != nil {
		return err
	}
	got, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || got != want {
		return fmt.Errorf("%w: stored request created %d evaluation jobs, want %d", harness.ErrStep, got, want)
	}
	return nil
}

func (s *pushSteps) policyNativeCheck(ctx context.Context, commit, name, state, message string) error {
	sha, err := s.commitHash(commit)
	if err != nil {
		return err
	}
	return deps.Eventually(ctx, "native ignored policy check", func(context.Context) (bool, error) {
		for _, check := range s.gitlab.fake.Checks("acme/platform", "infra") {
			if check.SHA == sha && check.Name == name && check.Status == state && strings.Contains(check.Description, message) {
				return true, nil
			}
		}
		return false, nil
	})
}

func (s *pushSteps) policyVersion(policy string) error {
	tag, err := strconv.Unquote(s.w.Last().Header.Get("ETag"))
	if err != nil {
		return fmt.Errorf("%w: policy snapshot has no version ETag", harness.ErrStep)
	}
	version, err := strconv.ParseInt(tag, 10, 64)
	if err != nil || version < 1 {
		return fmt.Errorf("%w: policy snapshot has an invalid version ETag", harness.ErrStep)
	}
	s.w.IDs[policy+"Version"] = tag
	return nil
}
