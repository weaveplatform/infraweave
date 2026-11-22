package sampling

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// Counts counts every retained sample, independently of the bounded metadata
// preview. Each batch is account/current-space qualified before blob-owner
// lifecycle checks; no staging, input, source or print is opened.
func (s *Service) Counts(ctx context.Context, account kernel.AccountID, policies, readable []kernel.ULID) (map[kernel.ULID]int64, error) {
	out := make(map[kernel.ULID]int64, len(policies))
	if len(policies) == 0 || len(readable) == 0 {
		return out, nil
	}
	const batch = 500
	at := s.d.Now()
	var after *kernel.ULID
	for {
		inputs, err := s.d.Store.CountableInputs(ctx, account, policies, readable, after, at, batch)
		if err != nil {
			return nil, fmt.Errorf("policies: count retained input candidates: %w", err)
		}
		if len(inputs) == 0 {
			return out, nil
		}
		keys := make([]string, len(inputs))
		for i, input := range inputs {
			keys[i] = input.Key
		}
		available, err := s.d.Objects.AvailableBatch(ctx, objectstore.PolicyInputs, keys, at)
		if err != nil {
			return nil, fmt.Errorf("policies: retained input availability: %w", err)
		}
		for _, input := range inputs {
			if available[input.Key] {
				out[input.Policy]++
			}
		}
		if len(inputs) < batch {
			return out, nil
		}
		after = new(inputs[len(inputs)-1].ID)
	}
}
