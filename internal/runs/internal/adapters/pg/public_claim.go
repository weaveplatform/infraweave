package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
)

// HoldingPublic uses the globally unique protocol worker, which has no account.
func (s *Store) HoldingPublic(ctx context.Context, worker kernel.ULID) (bool, error) {
	var holds bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs
		WHERE worker_id=$1 AND state=ANY($2))`, worker, onWorker).Scan(&holds)
	if err != nil {
		return false, fmt.Errorf("runs: read public worker assignment: %w", sql.Unavailable(err))
	}
	return holds, nil
}

// ProbePublicCandidate reads FIFO public work while excluding already full
// quotas and contended owners. Stable public bindings survive later assignments.
func (s *Store) ProbePublicCandidate(
	ctx context.Context,
	pool kernel.ULID,
	states []kernel.RunState,
	after *schedule.ClaimCursor,
	stacks []kernel.ULID,
	organizations []kernel.OrganizationID,
	accounts []kernel.AccountID,
) (lifecycle.Run, bool, error) {
	var at *time.Time
	var id *kernel.ULID
	if after != nil {
		at, id = &after.CreatedAt, &after.ID
	}
	if stacks == nil {
		stacks = []kernel.ULID{}
	}
	if organizations == nil {
		organizations = []kernel.OrganizationID{}
	}
	if accounts == nil {
		accounts = []kernel.AccountID{}
	}
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE worker_pool_id IS NULL AND organization_id IS NOT NULL
		AND (public_worker_pool_id IS NULL OR public_worker_pool_id=$1)
		AND state=ANY($2) AND NOT drift_detection AND priority=$3
		AND ($4::timestamptz IS NULL OR (created_at,id)>($4,$5::uuid))
		AND NOT(stack_id=ANY($6::uuid[])) AND NOT(organization_id=ANY($7::uuid[]))
		AND NOT(account_id=ANY($8::uuid[]))
		ORDER BY created_at,id LIMIT 1`, pool, names(states), string(kernel.RunPriorityNormal), at, id, stacks, organizations, accounts))
	if errors.Is(err, pgx.ErrNoRows) {
		return lifecycle.Run{}, false, nil
	}
	if err != nil {
		return lifecycle.Run{}, false, fmt.Errorf("runs: probe public queue: %w", sql.Unavailable(err))
	}
	return r, true, nil
}

// LockPublicCandidate revalidates the probed identity after quota and owner locks.
func (s *Store) LockPublicCandidate(ctx context.Context, probe lifecycle.Run, pool kernel.ULID, states []kernel.RunState) (lifecycle.Run, bool, error) {
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE account_id=$1 AND id=$2 AND stack_id=$3 AND state_version=$4 AND state=$5
		AND organization_id=$6 AND worker_pool_id IS NULL AND (public_worker_pool_id IS NULL OR public_worker_pool_id=$7)
		AND state=ANY($8) AND NOT drift_detection AND priority=$9 FOR UPDATE SKIP LOCKED`,
		probe.Account, probe.ID, probe.Stack, probe.Version, string(probe.State), probe.Organization, pool, names(states), string(kernel.RunPriorityNormal)))
	if errors.Is(err, pgx.ErrNoRows) {
		return lifecycle.Run{}, false, nil
	}
	if err != nil {
		return lifecycle.Run{}, false, fmt.Errorf("runs: lock public candidate %s: %w", probe.ID, sql.Unavailable(err))
	}
	return r, true, nil
}

// PublicUsage counts public executions across the owning organization's accounts.
func (s *Store) PublicUsage(ctx context.Context, org kernel.OrganizationID, account kernel.AccountID) (featuresapi.Usage, error) {
	var used featuresapi.Usage
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE account_id=$2)
		FROM runs WHERE organization_id=$1 AND worker_pool_id IS NULL AND public_worker_pool_id IS NOT NULL
		AND state=ANY($3)`, org, account, onWorker).Scan(&used.Organization, &used.Account)
	if err != nil {
		return used, fmt.Errorf("runs: count public executions: %w", sql.Unavailable(err))
	}
	return used, nil
}

// AssignPublic fixes the installation pool on first assignment and preserves it
// across every apply/replan, independently of mutable stack configuration.
func (s *Store) AssignPublic(ctx context.Context, run lifecycle.Run, worker, pool kernel.ULID, deadline time.Time) error {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE runs SET worker_id=$3,
		public_worker_pool_id=coalesce(public_worker_pool_id,$4), preparing_deadline=$5
		WHERE account_id=$1 AND id=$2 AND worker_pool_id IS NULL
		AND (public_worker_pool_id IS NULL OR public_worker_pool_id=$4)`, run.Account, run.ID, worker, pool, deadline)
	if err != nil {
		return fmt.Errorf("runs: bind public execution pool: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() != 1 {
		return lifecycle.ErrRefused
	}
	return nil
}

// ActivePool includes orphaned worker assignments until run recovery ends
// them, so deleting a worker never makes an in-flight ownership reset or
// hosting release safe. pool is a private or the public pool's ID.
func (s *Store) ActivePool(ctx context.Context, pool kernel.ULID) (bool, error) {
	var active bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs
		WHERE (public_worker_pool_id=$1 OR worker_pool_id=$1) AND state=ANY($2))`, pool, onWorker).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("runs: read pool assignments: %w", sql.Unavailable(err))
	}
	return active, nil
}
