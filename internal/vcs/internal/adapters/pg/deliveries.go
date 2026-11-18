package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

// Deliveries keeps verified deliveries and reads only webhook secrets, not VCS
// access credentials. Its writes join the caller's intake/job transaction.
type Deliveries struct{ store *Store }

// NewDeliveries uses the same sealed columns as integration rotation and replay.
func NewDeliveries(db tx.Querier, keys Sealer) *Deliveries { return &Deliveries{store: New(db, keys)} }

var _ intake.Store = (*Deliveries)(nil)

// Pending walks a bounded page without resetting at already-checked live jobs.
func (s *Deliveries) Pending(ctx context.Context, after kernel.ULID, limit int) ([]intake.Ref, error) {
	// A zero ULID's Valuer produces SQL NULL; UUID text keeps the first
	// page's all-zero lower bound inside the indexed range predicate.
	rows, err := tx.Q(ctx, s.store.db).Query(ctx, `SELECT account_id,id,replays,state FROM vcs_deliveries
		WHERE (state='received' OR (state='dispatched' AND last_error='')) AND id>$1 ORDER BY id LIMIT $2`, after.UUID(), limit)
	if err != nil {
		return nil, fmt.Errorf("vcs: read pending deliveries: %w", sql.Unavailable(err))
	}
	refs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (intake.Ref, error) {
		var ref intake.Ref
		err := row.Scan(&ref.Account, &ref.ID, &ref.Replays, &ref.State)
		return ref, err //nolint:wrapcheck // CollectRows' caller names the pending read
	})
	if err != nil {
		return nil, fmt.Errorf("vcs: read pending deliveries: %w", sql.Unavailable(err))
	}
	return refs, nil
}

// Integration opens only a previous secret whose validity is still in the future
// according to intake's clock; expired ciphertext cannot break current delivery.
func (s *Deliveries) Integration(ctx context.Context, id kernel.ULID, at time.Time) (intake.Integration, error) {
	in := intake.Integration{ID: id}
	var current, previous []byte
	err := tx.Q(ctx, s.store.db).QueryRow(ctx, `SELECT account_id, provider, webhook_secret_sealed,
		CASE WHEN previous_secret_valid_until > $2 THEN previous_webhook_secret_sealed END,
		previous_secret_valid_until FROM vcs_integrations WHERE id=$1`, id, at).
		Scan(&in.Account, &in.Provider, &current, &previous, &in.PreviousUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, intake.ErrNotFound
	}
	if err != nil {
		return in, fmt.Errorf("vcs: read webhook integration: %w", sql.Unavailable(err))
	}
	for _, c := range []struct {
		sealed []byte
		column int
		to     *[]byte
	}{{current, 1, &in.Current}, {previous, 2, &in.Previous}} {
		if len(c.sealed) == 0 {
			continue
		}
		col := webhookSecret
		if c.column == 2 {
			col = previousSecret
		}
		value, err := s.store.seal.OpenColumn(ctx, col, key(in.Account, id), c.sealed)
		if err != nil {
			return intake.Integration{}, fmt.Errorf("vcs: open webhook secret: %w", err)
		}
		*c.to = value.Reveal()
	}
	return in, nil
}

// Insert locks the account/provider-scoped integration's continued existence.
// Delivery rows deliberately survive an integration's later deletion: the
// immutable provider lets their already-verified jobs finish without its secret.
func (s *Deliveries) Insert(ctx context.Context, d intake.Delivery) (bool, error) {
	q := tx.Q(ctx, s.store.db)
	var id kernel.ULID
	err := q.QueryRow(ctx, `SELECT id FROM vcs_integrations
		WHERE account_id=$1 AND id=$2 AND provider=$3 FOR KEY SHARE`, d.Account, d.Integration, d.Provider).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, intake.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("vcs: hold webhook integration: %w", sql.Unavailable(err))
	}
	headers, err := json.Marshal(d.Headers)
	if err != nil {
		return false, fmt.Errorf("vcs: encode webhook headers: %w", err)
	}
	tag, err := q.Exec(ctx, `INSERT INTO vcs_deliveries
		(account_id,id,integration_id,provider,delivery_id,event,received_at,headers,signature,
		body,body_key,repository,state,deadline)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,nullif($11,''),$12,$13,$14)
		ON CONFLICT (integration_id,delivery_id) DO NOTHING`, d.Account, d.ID, d.Integration, d.Provider,
		d.DeliveryID, d.Event, d.ReceivedAt, headers, d.Signature, d.Body, d.BodyKey, d.Repository, d.State, d.Deadline)
	if err != nil {
		return false, fmt.Errorf("vcs: insert delivery: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected() == 1, nil
}

// Read answers the account-scoped delivery, independent of its integration.
func (s *Deliveries) Read(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (intake.Delivery, error) {
	d := intake.Delivery{Account: acct, ID: id}
	var headers []byte
	err := tx.Q(ctx, s.store.db).QueryRow(ctx, `SELECT integration_id,provider,delivery_id,event,
		received_at,headers,signature,body,coalesce(body_key,''),repository,state,deadline,runs_created,last_error,replays
		FROM vcs_deliveries WHERE account_id=$1 AND id=$2`, acct, id).Scan(&d.Integration, &d.Provider,
		&d.DeliveryID, &d.Event, &d.ReceivedAt, &headers, &d.Signature, &d.Body, &d.BodyKey, &d.Repository,
		&d.State, &d.Deadline, &d.RunsCreated, &d.LastError, &d.Replays)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, intake.ErrNotFound
	}
	if err != nil {
		return d, fmt.Errorf("vcs: read delivery: %w", sql.Unavailable(err))
	}
	if err := json.Unmarshal(headers, &d.Headers); err != nil {
		return d, fmt.Errorf("vcs: decode delivery headers: %w", err)
	}
	return d, nil
}

// State changes only a still-received delivery, so repeating an attempt cannot
// resurrect a completed delivery or overwrite its final result.
func (s *Deliveries) State(ctx context.Context, acct kernel.AccountID, id kernel.ULID, replays int64, state, lastError string) error {
	_, err := tx.Q(ctx, s.store.db).Exec(ctx, `UPDATE vcs_deliveries SET state=$3,last_error=$4
		WHERE account_id=$1 AND id=$2 AND state='received' AND replays=$5`, acct, id, state, lastError, replays)
	if err != nil {
		return fmt.Errorf("vcs: update delivery: %w", sql.Unavailable(err))
	}
	return nil
}

// Drop deletes one bounded retention/purge batch and returns its object keys for
// the same transaction's deferred platform blob deletion.
func (s *Deliveries) Drop(ctx context.Context, acct *kernel.AccountID, before *time.Time, batch int) ([]string, int, error) {
	rows, err := tx.Q(ctx, s.store.db).Query(ctx, `DELETE FROM vcs_deliveries WHERE (account_id,id) IN
		(SELECT account_id,id FROM vcs_deliveries WHERE ($1::uuid IS NULL OR account_id=$1)
		AND ($2::timestamptz IS NULL OR received_at<$2) ORDER BY received_at,id LIMIT $3 FOR UPDATE)
		RETURNING coalesce(body_key,'')`, acct, before, batch)
	if err != nil {
		return nil, 0, fmt.Errorf("vcs: prune deliveries: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	var keys []string
	n := 0
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, 0, fmt.Errorf("vcs: read pruned delivery: %w", sql.Unavailable(err))
		}
		n++
		if key != "" {
			keys = append(keys, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("vcs: prune deliveries: %w", sql.Unavailable(err))
	}
	return keys, n, nil
}
