package read

import (
	"context"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
)

type policyProjection struct {
	receipts []PolicyReceipt
	flags    []string
}

func (s *Service) policyEvidence(ctx context.Context, account kernel.AccountID, runs []kernel.ULID) (map[kernel.ULID]policyProjection, error) {
	snapshots := map[kernel.ULID]evidence.Snapshot{}
	if s.d.Evidence != nil {
		var err error
		snapshots, err = s.d.Evidence.PolicyEvidence(ctx, account, runs)
		if err != nil {
			return nil, err //nolint:wrapcheck // the evidence store names the read
		}
	}
	var policies, samples []kernel.ULID
	for _, snapshot := range snapshots {
		for _, receipt := range snapshot.Receipts {
			if receipt.SampleEvaluation != nil {
				samples = append(samples, *receipt.SampleEvaluation)
			}
			if !slices.Contains(policies, receipt.Policy) {
				policies = append(policies, receipt.Policy)
			}
		}
	}
	exists := map[kernel.ULID]bool{}
	if len(policies) != 0 {
		if s.d.Policies == nil {
			return nil, fmt.Errorf("runs: policy identity reader is unbound: %w", kernel.ErrUnavailable)
		}
		identities, err := s.d.Policies.Identities(ctx, account, policies)
		if err != nil {
			return nil, err //nolint:wrapcheck // the policy owner names the lookup
		}
		for id := range identities {
			exists[id] = true
		}
	}
	statuses := map[kernel.ULID]policyapi.SampleStatus{}
	if len(samples) > 0 {
		if s.d.Samples == nil {
			return nil, fmt.Errorf("runs: sample owner is unbound: %w", kernel.ErrUnavailable)
		}
		var err error
		statuses, err = s.d.Samples.SampleStatuses(ctx, account, samples)
		if err != nil {
			return nil, fmt.Errorf("runs: read policy sample status: %w", err)
		}
	}
	out := make(map[kernel.ULID]policyProjection, len(runs))
	for _, run := range runs {
		snapshot, ok := snapshots[run]
		if !ok {
			snapshot = evidence.Empty()
		}
		projection := policyProjection{receipts: []PolicyReceipt{}, flags: snapshot.Flags}
		for _, receipt := range snapshot.Receipts {
			status := policyapi.SampleStatus{}
			if receipt.SampleEvaluation != nil {
				status = statuses[*receipt.SampleEvaluation]
			}
			projection.receipts = append(
				projection.receipts,
				PolicyReceipt{Receipt: receipt, Deleted: !exists[receipt.Policy], Sampled: status.Sampled, SampleExpired: status.Expired},
			)
		}
		out[run] = projection
	}
	return out, nil
}
