package pg

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/weaveplatform/infraweave/internal/events/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

//go:embed migrations/*.sql
var files embed.FS

// Stream is the events domain's migration stream, after vcs.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations")
	return migrate.Stream{Name: "events", Files: sub}
}

// Held names the account reference the nightly orphan count checks.
// Event identities are retained history and deliberately survive pruning
// of the outbox's event, rather than holding that event alive.
func Held() []integrity.Held {
	return []integrity.Held{{Entity: "account", Table: "event_deliveries", Columns: []string{"account_id"}}}
}

// Store owns delivery records and joins the transaction on each context.
type Store struct{ db tx.Querier }

// New stores records in db, normally its primary pool.
func New(db tx.Querier) *Store { return &Store{db: db} }

// Insert records a previously unseen target in the enclosing transaction.
func (s *Store) Insert(ctx context.Context, d port.Delivery) (bool, error) {
	if !tx.In(ctx) {
		return false, queue.ErrNoTransaction
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `
INSERT INTO event_deliveries (account_id, event_id, sink, target_key, rule_index, created_at)
VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
		d.Account, d.EventID, d.Sink, d.Target, d.RuleIndex, d.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("insert event delivery: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected() != 0, nil
}

// Prune deletes at most limit records older than before.
func (s *Store) Prune(ctx context.Context, before time.Time, limit int) (int64, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `
WITH old AS (
    SELECT ctid FROM event_deliveries WHERE created_at < $1
    ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)
DELETE FROM event_deliveries d USING old WHERE d.ctid = old.ctid`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("prune event deliveries: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected(), nil
}

// Purge deletes at most limit records belonging to account.
func (s *Store) Purge(ctx context.Context, account kernel.ULID, limit int) (int64, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `
WITH old AS (
    SELECT ctid FROM event_deliveries WHERE account_id = $1
    LIMIT $2 FOR UPDATE)
DELETE FROM event_deliveries d USING old WHERE d.ctid = old.ctid`, account, limit)
	if err != nil {
		return 0, fmt.Errorf("purge event deliveries: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected(), nil
}

var _ port.Store = (*Store)(nil)
