package sampling

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

// Reference carries only safe publication metadata, including policy deletion.
type Reference struct {
	Input        Input
	PolicyExists bool
}

// SampleStatuses never treats pending, failed or over-budget evidence as sampled.
// It enforces the evaluation-time ceiling even before blob.expire sweeps.
func (s *Service) SampleStatuses(ctx context.Context, account kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]policyapi.SampleStatus, error) {
	refs, err := s.d.Store.References(ctx, account, ids)
	if err != nil {
		return nil, fmt.Errorf("policies: read retained sample status: %w", err)
	}
	out := make(map[kernel.ULID]policyapi.SampleStatus, len(refs))
	for id, ref := range refs {
		retained := ref.Input.PublishedAt != nil
		available := false
		if retained && ref.PolicyExists && ref.Input.State == "live" && s.d.Now().Before(ref.Input.ExpiresAt) {
			available, err = s.d.Objects.Available(ctx, objectstore.PolicyInputs, ref.Input.Key, s.d.Now())
			if err != nil {
				return nil, fmt.Errorf("policies: read retained sample status: %w", err)
			}
		}
		out[id] = policyapi.SampleStatus{Sampled: available, Expired: retained && !available}
	}
	return out, nil
}
