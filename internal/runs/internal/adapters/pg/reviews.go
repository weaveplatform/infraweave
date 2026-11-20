package pg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
)

var _ review.Store = (*Store)(nil)

// RecordReview commits a vote only under the current run's state/version fence.
func (s *Store) RecordReview(ctx context.Context, record review.Record) error {
	if !tx.In(ctx) {
		return lifecycle.ErrNoTransaction
	}
	if !record.Valid() {
		return review.ErrReview
	}
	run, err := s.Lock(ctx, record.Account, record.Run)
	if err != nil {
		return err
	}
	if run.Version != record.StateVersion || run.State != record.State {
		return &lifecycle.Conflict{Run: run.ID, State: run.State, Version: run.Version}
	}
	facts, err := json.Marshal(record.Facts)
	if err != nil {
		return fmt.Errorf("runs: encode review metadata: %w", kernel.ErrUnavailable)
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO run_reviews
  (account_id,run_id,id,author_key,caller,session_id,decision,note,state,state_version,created_at,request_id,facts)
  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		record.Account, record.Run, record.ID, record.AuthorKey, record.Caller, record.Session, string(record.Decision), record.Note,
		string(record.State), record.StateVersion, record.CreatedAt, record.RequestID, facts)
	if err != nil {
		return fmt.Errorf("runs: record review: %w", sql.Unavailable(err))
	}
	return nil
}

// Reviews returns each account-owned run's complete immutable review history.
func (s *Store) Reviews(ctx context.Context, account kernel.AccountID, runs []kernel.ULID) (map[kernel.ULID][]review.Record, error) {
	out := map[kernel.ULID][]review.Record{}
	owned, err := tx.Q(ctx, s.db).Query(ctx, `SELECT id FROM runs WHERE account_id=$1 AND id=ANY($2)`, account, runs)
	if err != nil {
		return nil, fmt.Errorf("runs: read reviewed runs: %w", sql.Unavailable(err))
	}
	for owned.Next() {
		var id kernel.ULID
		if err := owned.Scan(&id); err != nil {
			owned.Close()
			return nil, fmt.Errorf("runs: scan reviewed run: %w", sql.Unavailable(err))
		}
		out[id] = []review.Record{}
	}
	err = owned.Err()
	owned.Close()
	if err != nil {
		return nil, fmt.Errorf("runs: read reviewed run rows: %w", sql.Unavailable(err))
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT run_id,id,author_key,caller,session_id,decision,note,state,state_version,created_at,request_id,facts
  FROM run_reviews WHERE account_id=$1 AND run_id=ANY($2) ORDER BY run_id,created_at,id`, account, runs)
	if err != nil {
		return nil, fmt.Errorf("runs: read reviews: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	for rows.Next() {
		r := review.Record{Account: account}
		var decision, state string
		var facts []byte
		if err := rows.Scan(&r.Run, &r.ID, &r.AuthorKey, &r.Caller, &r.Session, &decision, &r.Note, &state, &r.StateVersion, &r.CreatedAt, &r.RequestID, &facts); err != nil {
			return nil, fmt.Errorf("runs: scan review: %w", sql.Unavailable(err))
		}
		r.Decision = review.Decision(decision)
		r.State = kernel.RunState(state)
		r.CreatedAt = r.CreatedAt.UTC()
		decoder := json.NewDecoder(bytes.NewReader(facts))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&r.Facts); err != nil || !r.Valid() {
			return nil, fmt.Errorf("runs: invalid stored review metadata: %w", kernel.ErrUnavailable)
		}
		out[r.Run] = append(out[r.Run], r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read review rows: %w", sql.Unavailable(err))
	}
	return out, nil
}
