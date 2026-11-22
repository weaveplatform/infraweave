package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// HoldSampleLink fences deletion without blocking ordinary non-key state writes.
// Stack ownership has already been held through the stacks domain's port.
func (s *Store) HoldSampleLink(ctx context.Context, account kernel.AccountID, stack, run, receipt kernel.ULID) (read.SampleLink, error) {
	if !tx.In(ctx) {
		return read.SampleLink{}, lifecycle.ErrNoTransaction
	}
	var owned kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id FROM runs WHERE account_id=$1 AND stack_id=$2 AND id=$3 FOR KEY SHARE`, account, stack, run).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return read.SampleLink{}, policyapi.ErrSampleMissing
	}
	if err != nil {
		return read.SampleLink{}, fmt.Errorf("runs: hold sample run: %w", sql.Unavailable(err))
	}
	var out read.SampleLink
	var evaluation *kernel.ULID
	err = tx.Q(ctx, s.db).QueryRow(ctx, `SELECT policy_id,sample_evaluation_id FROM run_policy_receipts
        WHERE account_id=$1 AND run_id=$2 AND id=$3 FOR KEY SHARE`, account, run, receipt).Scan(&out.Policy, &evaluation)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && evaluation == nil {
		return read.SampleLink{}, policyapi.ErrSampleMissing
	}
	if err != nil {
		return read.SampleLink{}, fmt.Errorf("runs: hold sample receipt: %w", sql.Unavailable(err))
	}
	out.Evaluation = *evaluation
	return out, nil
}
