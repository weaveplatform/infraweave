package pg

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// HoldPolicyTriggers serializes trigger fan-out within an account before any
// source/target locks. Two terminal sources targeting each other cannot acquire
// their owner rows in opposite order. Other workflow kinds do not take it.
func (s *Store) HoldPolicyTriggers(ctx context.Context, account kernel.AccountID) error {
	if !tx.In(ctx) {
		return lifecycle.ErrNoTransaction
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "runs.policy-trigger:"+account.String())
	if err != nil {
		return fmt.Errorf("runs: fence trigger fan-out: %w", sql.Unavailable(err))
	}
	return nil
}

// QueuedForPolicyTrigger returns an explicit zero for each supplied owner ID
// without joining stacks or borrowing another domain's transport count.
func (s *Store) QueuedForPolicyTrigger(ctx context.Context, account kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID]int64, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT s.id,count(r.id)
		FROM unnest($2::uuid[]) s(id)
		LEFT JOIN runs r ON r.account_id=$1 AND r.stack_id=s.id AND r.state='QUEUED'
		GROUP BY s.id`, account, stacks)
	if err != nil {
		return nil, fmt.Errorf("runs: count trigger target queues: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	counts := make(map[kernel.ULID]int64, len(stacks))
	for rows.Next() {
		var id kernel.ULID
		var count int64
		if err := rows.Scan(&id, &count); err != nil {
			return nil, fmt.Errorf("runs: read trigger target queue: %w", sql.Unavailable(err))
		}
		counts[id] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read trigger target queues: %w", sql.Unavailable(err))
	}
	return counts, nil
}
