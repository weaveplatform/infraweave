package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

// CaptureSlot consumes one refresh per DB-clock minute, independently of sample
// quotas. It holds the head but does not reference an input before it exists.
func (s *Store) CaptureSlot(ctx context.Context, account kernel.AccountID, space kernel.ULID, kind string) (sampling.CaptureSlot, bool, error) {
	if !tx.In(ctx) {
		return sampling.CaptureSlot{}, false, queue.ErrNoTransaction
	}
	var slot sampling.CaptureSlot
	err := tx.Q(ctx, s.db).QueryRow(ctx, `INSERT INTO policy_input_capture_heads(account_id,space_snapshot_id,capture_kind,reserved_at)
        VALUES($1,$2,$3,statement_timestamp())
        ON CONFLICT(account_id,space_snapshot_id,capture_kind) DO UPDATE SET reserved_at=excluded.reserved_at
        WHERE policy_input_capture_heads.reserved_at<=excluded.reserved_at-interval '1 minute'
        RETURNING reserved_at,current_input_id,pending_input_id`, account, space, kind).Scan(&slot.At, &slot.Current, &slot.Pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return slot, false, nil
	}
	if err != nil {
		return slot, false, fmt.Errorf("policies: reserve capture refresh: %w", sql.Unavailable(err))
	}
	return slot, true, nil
}

// SetCapturePending joins the held head after its staged input was inserted.
func (s *Store) SetCapturePending(ctx context.Context, input sampling.Input) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE policy_input_capture_heads SET pending_input_id=$4
        WHERE account_id=$1 AND space_snapshot_id=$2 AND capture_kind=$3`, input.Account, input.SpaceSnapshot, input.CaptureKind, input.ID)
	if err != nil {
		return fmt.Errorf("policies: stage capture head: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() != 1 {
		return sampling.ErrNotFound
	}
	return nil
}

// lockInputHead probes immutable identity without locking the input first. An
// account purge may already have removed the head; that orphan cannot publish.
func (s *Store) lockInputHead(ctx context.Context, account kernel.AccountID, id kernel.ULID) error {
	var space *kernel.ULID
	var kind string
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT space_snapshot_id,capture_kind FROM policy_input_objects WHERE account_id=$1 AND id=$2`, account, id).Scan(&space, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return sampling.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("policies: read input owner: %w", sql.Unavailable(err))
	}
	if space == nil {
		return nil
	}
	var found int
	err = tx.Q(ctx, s.db).
		QueryRow(ctx, `SELECT 1 FROM policy_input_capture_heads WHERE account_id=$1 AND space_snapshot_id=$2 AND capture_kind=$3 FOR UPDATE`, account, space, kind).
		Scan(&found)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("policies: hold capture head: %w", sql.Unavailable(err))
	}
	return nil
}

// PublishCapture changes the readable head only for its exact pending input.
// Input(lock=true) has already acquired head -> input locks in that order.
func (s *Store) PublishCapture(ctx context.Context, input sampling.Input) (*kernel.ULID, bool, error) {
	if !tx.In(ctx) {
		return nil, false, queue.ErrNoTransaction
	}
	var current *kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT current_input_id FROM policy_input_capture_heads
        WHERE account_id=$1 AND space_snapshot_id=$2 AND capture_kind=$3 AND pending_input_id=$4 FOR UPDATE`, input.Account, input.SpaceSnapshot, input.CaptureKind, input.ID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("policies: hold pending capture: %w", sql.Unavailable(err))
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `UPDATE policy_input_capture_heads SET current_input_id=$4,pending_input_id=NULL
        WHERE account_id=$1 AND space_snapshot_id=$2 AND capture_kind=$3 AND pending_input_id=$4`, input.Account, input.SpaceSnapshot, input.CaptureKind, input.ID)
	if err != nil {
		return nil, false, fmt.Errorf("policies: publish capture: %w", sql.Unavailable(err))
	}
	return current, true, nil
}

// ClearCapturePending never clears a newer replacement or the current capture.
func (s *Store) ClearCapturePending(ctx context.Context, input sampling.Input) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if input.SpaceSnapshot == nil {
		return nil
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE policy_input_capture_heads SET pending_input_id=NULL
        WHERE account_id=$1 AND space_snapshot_id=$2 AND capture_kind=$3 AND pending_input_id=$4`, input.Account, input.SpaceSnapshot, input.CaptureKind, input.ID)
	if err != nil {
		return fmt.Errorf("policies: settle pending capture: %w", sql.Unavailable(err))
	}
	return nil
}
