package core

import (
	"context"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// EvidenceProjection keeps safe evaluation history separate from live retention.
type EvidenceProjection struct {
	Records      map[kernel.ULID][]EvaluationRecord
	SampleCounts map[kernel.ULID]int64
}

// ProjectionEvidence composes current owned metadata through one DB snapshot.
// Availability is lifecycle metadata only, so serialization retry performs no
// external object read or side effect. Deleted parents naturally contribute zero.
func (s *Service) ProjectionEvidence(ctx context.Context, p kernel.Principal, ids []kernel.ULID, stack *kernel.ULID, counts bool) (EvidenceProjection, error) {
	out := EvidenceProjection{Records: map[kernel.ULID][]EvaluationRecord{}, SampleCounts: map[kernel.ULID]int64{}}
	if len(ids) == 0 {
		return out, nil
	}
	readable, err := s.deps.Authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return out, fmt.Errorf("policies: readable projection spaces: %w", err)
	}
	if s.deps.Evidence == nil {
		return out, kernel.ErrUnavailable
	}
	err = tx.Do(ctx, s.deps.DB, tx.Options{Serializable: true}, func(ctx context.Context) error {
		var err error
		out.Records, err = s.deps.Evidence.EvaluationPreviews(ctx, p.AccountID, ids, readable, stack, s.deps.Now())
		if err != nil {
			return fmt.Errorf("policies: evaluation preview: %w", err)
		}
		out.SampleCounts = map[kernel.ULID]int64{}
		if counts && s.deps.Sampling != nil {
			out.SampleCounts, err = s.deps.Sampling.Counts(ctx, p.AccountID, ids, readable)
		}
		return err //nolint:wrapcheck // sample owner names the count
	})
	if err != nil {
		return EvidenceProjection{}, fmt.Errorf("policies: project evidence: %w", err)
	}
	return out, nil
}
