package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

var _ intake.AdminStore = (*Deliveries)(nil)

func (s *Deliveries) owner(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) error {
	query := `SELECT id FROM vcs_integrations WHERE account_id=$1 AND id=$2`
	if lock {
		query += " FOR SHARE"
	}
	var found kernel.ULID
	err := tx.Q(ctx, s.store.db).QueryRow(ctx, query, acct, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return intake.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("vcs: hold delivery integration: %w", sql.Unavailable(err))
	}
	return nil
}

// Owned restricts both parent and child to the account, then locks rotation and
// replay when requested. Original receipt time, not replay time, bounds retention.
func (s *Deliveries) Owned(ctx context.Context, acct kernel.AccountID, integration, id kernel.ULID, before time.Time, lock bool) (intake.Delivery, error) {
	if lock && !tx.In(ctx) {
		return intake.Delivery{}, queue.ErrNoTransaction
	}
	if err := s.owner(ctx, acct, integration, lock); err != nil {
		return intake.Delivery{}, err
	}
	if lock {
		var held kernel.ULID
		err := tx.Q(ctx, s.store.db).
			QueryRow(ctx, `SELECT id FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2 AND id=$3 AND received_at >= $4 FOR UPDATE`, acct, integration, id, before).
			Scan(&held)
		if errors.Is(err, pgx.ErrNoRows) {
			return intake.Delivery{}, intake.ErrNotFound
		}
		if err != nil {
			return intake.Delivery{}, fmt.Errorf("vcs: lock replay delivery: %w", sql.Unavailable(err))
		}
	}
	d, err := s.Read(ctx, acct, id)
	if err != nil {
		return d, err
	}
	if d.Integration != integration || d.ReceivedAt.Before(before) {
		return intake.Delivery{}, intake.ErrNotFound
	}
	return d, nil
}

// List selects metadata only. Scope is part of cursor identity, so a cursor
// cannot be transferred to another integration or account.
//
//nolint:wrapcheck // Find names query/cursor errors and its scanner is internal
func (s *Deliveries) List(ctx context.Context, acct kernel.AccountID, integration kernel.ULID, before time.Time, in search.Input) (search.Result[intake.Delivery], error) {
	if err := s.owner(ctx, acct, integration, false); err != nil {
		return search.Result[intake.Delivery]{}, err
	}
	entity := sqlsearch.Entity{
		List: "vcs-deliveries/" + acct.String() + "/" + integration.String(), From: "vcs_deliveries d",
		Select: "d.id,d.delivery_id,d.event,d.received_at,d.state,d.last_error,d.runs_created,d.replays",
		Space:  "d.account_id", ID: "d.id::text",
		Columns: []sqlsearch.Column{
			{Field: search.Field{Name: "receivedAt", Type: search.Time, Orderable: true}, Expr: "d.received_at", Cast: "timestamptz"},
			{Field: search.Field{Name: "id", Type: search.String, Orderable: true}, Expr: "d.id::text", Cast: "text"},
		},
		Order: []search.Order{{Field: "receivedAt", Desc: true}, {Field: "id", Desc: true}},
	}
	scope := sqlsearch.Scope{Where: "d.account_id=$1 AND d.integration_id=$2 AND d.received_at >= $3", Args: []any{acct, integration, before}, Reachable: []kernel.ULID{acct.ULID}}
	return sqlsearch.Find(ctx, tx.Q(ctx, s.store.db), entity, scope, in, func(row pgx.Row, extra ...any) (intake.Delivery, error) {
		d := intake.Delivery{Account: acct, Integration: integration}
		dest := make([]any, 0, 8+len(extra))
		dest = append(dest, &d.ID, &d.DeliveryID, &d.Event, &d.ReceivedAt, &d.State, &d.LastError, &d.RunsCreated, &d.Replays)
		err := row.Scan(append(dest, extra...)...)
		return d, err
	})
}

// Hold fences preparation against a replay before any prepared row/child write.
func (s *Deliveries) Hold(ctx context.Context, ref intake.Ref) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	var id kernel.ULID
	err := tx.Q(ctx, s.store.db).
		QueryRow(ctx, `SELECT id FROM vcs_deliveries WHERE account_id=$1 AND id=$2 AND replays=$3 AND state=$4 FOR KEY SHARE`, ref.Account, ref.ID, ref.Replays, ref.State).
		Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("vcs: hold delivery generation: %w", sql.Unavailable(err))
	}
	return true, nil
}

// Failure latches a safe exhausted-work outcome for just this generation.
// A dispatched delivery remains dispatchable by its other stack children.
func (s *Deliveries) Failure(ctx context.Context, ref intake.Ref) error {
	_, err := tx.Q(ctx, s.store.db).Exec(ctx, `UPDATE vcs_deliveries SET last_error=$4,
 state=CASE WHEN state='received' THEN 'failed' ELSE state END
 WHERE account_id=$1 AND id=$2 AND replays=$3 AND state IN ('received','dispatched')`, ref.Account, ref.ID, ref.Replays, intake.ProcessingFailed)
	if err != nil {
		return fmt.Errorf("vcs: record delivery failure: %w", sql.Unavailable(err))
	}
	return nil
}

// EvaluationFailed records a child failure without changing sibling eligibility.
func (s *Store) EvaluationFailed(ctx context.Context, acct kernel.AccountID, id kernel.ULID, replays int64) error {
	return (&Deliveries{store: s}).Failure(ctx, intake.Ref{Account: acct, ID: id, Replays: replays})
}

// Reset clears only prepared facts and counters; existing runs and original
// bytes/receipt identity are immutable across replays.
func (s *Deliveries) Reset(ctx context.Context, d intake.Delivery, deadline time.Time) (intake.Delivery, error) {
	if !tx.In(ctx) {
		return intake.Delivery{}, queue.ErrNoTransaction
	}
	err := tx.Q(ctx, s.store.db).QueryRow(ctx, `UPDATE vcs_deliveries SET state='received',deadline=$4,runs_created=0,last_error='',replays=replays+1
 WHERE account_id=$1 AND id=$2 AND replays=$3 RETURNING replays`, d.Account, d.ID, d.Replays, deadline).Scan(&d.Replays)
	if errors.Is(err, pgx.ErrNoRows) {
		return intake.Delivery{}, &intake.ReplayConflict{Detail: "The delivery changed while replay was being requested."}
	}
	if err != nil {
		return intake.Delivery{}, fmt.Errorf("vcs: reset delivery: %w", sql.Unavailable(err))
	}
	if _, err = tx.Q(ctx, s.store.db).Exec(ctx, `DELETE FROM vcs_prepared_events WHERE account_id=$1 AND delivery_id=$2`, d.Account, d.ID); err != nil {
		return intake.Delivery{}, fmt.Errorf("vcs: reset prepared events: %w", sql.Unavailable(err))
	}
	d.State, d.LastError, d.RunsCreated, d.Deadline = intake.Received, "", 0, deadline
	return d, nil
}

// Audit joins the same transaction as the reset and new processing job.
func (s *Deliveries) Audit(ctx context.Context, entry kernel.AuditEntry) error {
	return s.store.Audit(ctx, entry)
}
