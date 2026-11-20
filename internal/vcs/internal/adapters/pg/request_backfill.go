package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

var _ push.RequestBackfiller = (*Store)(nil)

// BackfillRequest keeps the first native snapshot under the child transaction's
// queue/replay fence. Comparing the original event prevents a stale preparation
// from replacing a different event; concurrent children reuse the winner.
func (s *Store) BackfillRequest(
	ctx context.Context,
	account kernel.AccountID,
	delivery kernel.ULID,
	number int,
	original vcsapi.Event,
	prepared push.Prepared,
) (push.Prepared, error) {
	if !tx.In(ctx) {
		return push.Prepared{}, push.ErrNoTransaction
	}
	if number < 0 || prepared.Request == nil {
		return push.Prepared{}, push.ErrRequestChanged
	}
	expected, err := json.Marshal(original)
	if err != nil {
		return push.Prepared{}, fmt.Errorf("VCS request backfill identity: %w", err)
	}
	data, err := json.Marshal(prepared)
	if err != nil {
		return push.Prepared{}, fmt.Errorf("VCS request backfill facts: %w", err)
	}
	var body []byte
	err = tx.Q(ctx, s.db).QueryRow(ctx, `UPDATE vcs_prepared_events SET event=$4
 WHERE account_id=$1 AND delivery_id=$2 AND event_no=$3
 AND (event->'request' IS NULL OR event->'request'='null'::jsonb)
 AND event->'event'=$5::jsonb RETURNING event`, account, delivery, number, data, expected).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.Q(ctx, s.db).
			QueryRow(ctx, `SELECT event FROM vcs_prepared_events WHERE account_id=$1 AND delivery_id=$2 AND event_no=$3 FOR UPDATE`, account, delivery, number).
			Scan(&body)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return push.Prepared{}, push.ErrRequestChanged
	}
	if err != nil {
		return push.Prepared{}, fmt.Errorf("VCS request backfill write: %w", sql.Unavailable(err))
	}
	var current push.Prepared
	if err := json.Unmarshal(body, &current); err != nil {
		return push.Prepared{}, fmt.Errorf("VCS request backfill document: %w", err)
	}
	if current.Request == nil {
		return push.Prepared{}, push.ErrRequestChanged
	}
	return current, nil
}
