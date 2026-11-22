package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

// Capture holds head before input when requested, without opening staging.
func (s *Store) Capture(ctx context.Context, account kernel.AccountID, id kernel.ULID, lock bool) (sampling.Input, bool, error) {
	input, err := s.InspectInput(ctx, account, id, lock)
	if err != nil {
		return sampling.Input{}, false, err //nolint:wrapcheck // the input owner names the failure
	}
	if input.SpaceSnapshot == nil {
		return sampling.Input{}, false, sampling.ErrNotFound
	}
	var current *kernel.ULID
	err = tx.Q(ctx, s.db).QueryRow(ctx, `SELECT current_input_id FROM policy_input_capture_heads
        WHERE account_id=$1 AND space_snapshot_id=$2 AND capture_kind=$3`, account, input.SpaceSnapshot, input.CaptureKind).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && current == nil {
		return sampling.Input{}, false, sampling.ErrNotFound
	}
	if err != nil {
		return sampling.Input{}, false, fmt.Errorf("policies: read capture head: %w", err)
	}
	return input, *current == id, nil
}

// ListCaptures never selects staging or object keys, and excludes a pending
// replacement until it is fully published. Current-head CAS fences its reader.
func (s *Store) ListCaptures(ctx context.Context, account kernel.AccountID, space kernel.ULID, kind string, in search.Input) (search.Result[core.Capture], error) {
	if kind == "TRIGGER" {
		kind = "TRIGGER_RUN_FINISH"
	}
	entity := sqlsearch.Entity{
		List: "policy-input-captures", From: "policy_input_capture_heads h JOIN policy_input_objects i ON i.account_id=h.account_id AND i.id=h.current_input_id",
		Select: "i.id,i.capture_kind,i.created_at", Space: "h.space_snapshot_id", ID: "i.id::text",
		Columns: []sqlsearch.Column{{Field: search.Field{Name: "createdAt", Type: search.Time, Orderable: true}, Expr: "i.created_at", Cast: "timestamptz"}},
		Order:   []search.Order{{Field: "createdAt", Desc: true}},
	}
	scope := sqlsearch.Scope{
		Where:     "h.account_id=$1 AND h.capture_kind=$2 AND i.state='live' AND i.published_at IS NOT NULL AND i.expires_at>statement_timestamp()",
		Args:      []any{account, kind},
		Reachable: []kernel.ULID{space},
	}
	scan := func(row pgx.Row, extra ...any) (core.Capture, error) {
		var out core.Capture
		err := row.Scan(append([]any{&out.ID, &out.Kind, &out.At}, extra...)...)
		return out, err //nolint:wrapcheck // the query names the operation
	}
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), entity, scope, in, scan)
	if err != nil {
		return out, fmt.Errorf("policies: list input captures: %w", err)
	}
	return out, nil
}
