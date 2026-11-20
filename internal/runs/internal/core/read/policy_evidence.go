package read

import (
	"context"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
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
	var policies []kernel.ULID
	for _, snapshot := range snapshots {
		for _, receipt := range snapshot.Receipts {
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
	out := make(map[kernel.ULID]policyProjection, len(runs))
	for _, run := range runs {
		snapshot, ok := snapshots[run]
		if !ok {
			snapshot = evidence.Empty()
		}
		projection := policyProjection{receipts: []PolicyReceipt{}, flags: snapshot.Flags}
		for _, receipt := range snapshot.Receipts {
			projection.receipts = append(projection.receipts, PolicyReceipt{Receipt: receipt, Deleted: !exists[receipt.Policy]})
		}
		out[run] = projection
	}
	return out, nil
}
