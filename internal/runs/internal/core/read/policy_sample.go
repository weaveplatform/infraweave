package read

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// SampleLink is an exact immutable receipt's policy/evaluation reference.
type SampleLink struct{ Policy, Evaluation kernel.ULID }

// SampleOwners holds run then receipt beneath the stack owner's deletion fence.
type SampleOwners interface {
	HoldSampleLink(context.Context, kernel.AccountID, kernel.ULID, kernel.ULID, kernel.ULID) (SampleLink, error)
}

// PolicySample holds stack->run->receipt->policy->blob without replaying external
// reads. Failures are audited only after the true outer transaction ends.
func (s *Service) PolicySample(ctx context.Context, p kernel.Principal, run, receipt string) (policyapi.Sample, error) {
	if s.d.SampleReader == nil || s.d.SampleOwners == nil {
		return policyapi.Sample{}, kernel.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out policyapi.Sample
	err := tx.Do(ctx, s.d.DB, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		r, err := s.get(ctx, p.AccountID, run)
		if errors.Is(err, ErrNotFound) {
			return policyapi.ErrSampleMissing
		}
		if err != nil {
			return fmt.Errorf("runs: locate sample owner: %w", err)
		}
		id, err := kernel.ParseULID(receipt)
		if err != nil {
			return policyapi.ErrSampleMissing
		}
		if err := s.d.Stacks.Hold(ctx, p.AccountID, r.Stack); err != nil {
			if errors.Is(err, stacksapi.ErrNotFound) {
				return policyapi.ErrSampleMissing
			}
			return fmt.Errorf("runs: hold sample stack: %w", err)
		}
		stack, err := s.d.Stacks.Get(ctx, p.AccountID, r.Stack)
		if err != nil {
			return fmt.Errorf("runs: resolve sample stack: %w", err)
		}
		ok, err := s.d.Authz.Authorize(ctx, p, accessapi.ActionSpaceRead, stack.Space)
		if err != nil {
			return fmt.Errorf("runs: authorize sample owner: %w", err)
		}
		if !ok {
			return &policyapi.SampleDenied{Action: string(accessapi.ActionSpaceRead)}
		}
		link, err := s.d.SampleOwners.HoldSampleLink(ctx, p.AccountID, r.Stack, r.ID, id)
		if err != nil {
			return fmt.Errorf("runs: hold receipt sample link: %w", err)
		}
		out, err = s.d.SampleReader.ReadSample(ctx, p, link.Policy, link.Evaluation)
		return err //nolint:wrapcheck // policy owner controls evidence authorization
	})
	if err != nil {
		return policyapi.Sample{}, s.d.SampleReader.ReadFailure(ctx, p, err) //nolint:wrapcheck // safe owner-domain classification
	}
	return out, nil
}
