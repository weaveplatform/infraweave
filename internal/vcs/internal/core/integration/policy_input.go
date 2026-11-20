package integration

import (
	"context"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

// PolicyIntegration reads current descriptive metadata without opening any
// provider credential or webhook secret. The evaluator holds the integration.
func (s *Service) PolicyIntegration(ctx context.Context, account kernel.AccountID, id kernel.ULID) (policyapi.VCSIntegrationFacts, error) {
	in, err := s.store.Get(ctx, account, id, false)
	if err != nil {
		return policyapi.VCSIntegrationFacts{}, fmt.Errorf("VCS policy integration metadata: %w", err)
	}
	if s.policySpaces == nil {
		return policyapi.VCSIntegrationFacts{}, fmt.Errorf("%w: VCS policy space owner is unavailable", kernel.ErrUnavailable)
	}
	space, err := s.policySpaces.PolicySpace(ctx, account, in.Space)
	if err != nil {
		return policyapi.VCSIntegrationFacts{}, fmt.Errorf("VCS policy integration space: %w", err)
	}
	labels := slices.Clone(space.Labels)
	spaceFacts := policyapi.SpaceFacts{ID: space.Slug, Name: space.Name, Labels: policyapi.Fact[[]string]{Known: true, Value: &labels}}
	return policyapi.VCSIntegrationFacts{
		ID: in.ID, Name: in.Name, Description: in.Description, Labels: slices.Clone(in.Labels), Provider: in.Provider,
		IsDefault: in.IsDefault, CreatedAt: in.CreatedAt, UpdatedAt: in.UpdatedAt,
		Space: policyapi.Fact[policyapi.SpaceFacts]{Known: true, Value: &spaceFacts},
	}, nil
}
