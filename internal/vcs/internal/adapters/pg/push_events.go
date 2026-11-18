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
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

// EventContext reads no body or credentials while checking a child's owner.
func (s *Store) EventContext(ctx context.Context, acct kernel.AccountID, delivery kernel.ULID) (push.EventContext, bool, error) {
	var out push.EventContext
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT integration_id,provider,state,deadline FROM vcs_deliveries
		WHERE account_id=$1 AND id=$2`, acct, delivery).Scan(&out.Integration, &out.Provider, &out.State, &out.Deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, fmt.Errorf("vcs: read event context: %w", sql.Unavailable(err))
	}
	return out, true, nil
}

// PutPrepared persists the shared full event data, never a queue payload copy.
func (s *Store) PutPrepared(ctx context.Context, acct kernel.AccountID, delivery kernel.ULID, events []push.Prepared) error {
	if !tx.In(ctx) {
		return push.ErrNoTransaction
	}
	for number, event := range events {
		body, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("vcs: encode prepared event: %w", err)
		}
		if _, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_prepared_events(account_id,delivery_id,event_no,event)
			VALUES($1,$2,$3,$4) ON CONFLICT(account_id,delivery_id,event_no) DO NOTHING`, acct, delivery, number, body); err != nil {
			return fmt.Errorf("vcs: store prepared event: %w", sql.Unavailable(err))
		}
	}
	return nil
}

// Prepared preserves provider order, and returns none after delivery retention.
func (s *Store) Prepared(ctx context.Context, acct kernel.AccountID, delivery kernel.ULID) ([]push.Prepared, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT event FROM vcs_prepared_events
		WHERE account_id=$1 AND delivery_id=$2 ORDER BY event_no`, acct, delivery)
	if err != nil {
		return nil, fmt.Errorf("vcs: read prepared events: %w", sql.Unavailable(err))
	}
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (push.Prepared, error) {
		var body []byte
		var event push.Prepared
		if err := row.Scan(&body); err != nil {
			return event, err //nolint:wrapcheck // CollectRows caller names the read
		}
		err := json.Unmarshal(body, &event)
		return event, err //nolint:wrapcheck // CollectRows caller names the stored document
	})
	if err != nil {
		return nil, fmt.Errorf("vcs: collect prepared events: %w", sql.Unavailable(err))
	}
	return events, nil
}
