package vcs

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

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
