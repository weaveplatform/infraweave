package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/events/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Constraint failures roll back the caller, while an unavailable store
// cannot report successful cleanup or a harmless duplicate.
func TestAccDeliveryStoreFailures(t *testing.T) {
	f := open(t)
	err := tx.Do(t.Context(), f.pool, tx.Options{}, func(ctx context.Context) error {
		_, err := f.store.Insert(ctx, port.Delivery{Account: f.event.Account, EventID: f.event.ID, Sink: "commit_checks", Target: "", CreatedAt: time.Now()})
		return err
	})
	pgerr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgerr.Code != "23514" || errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("constraint failure = %v", err)
	}
	if n := f.count(t, "SELECT count(*) FROM event_deliveries"); n != 0 {
		t.Fatalf("refused insert left %d rows", n)
	}
	f.pool.Close()
	if _, err := f.store.Prune(t.Context(), time.Now(), 1); err == nil {
		t.Fatal("prune succeeded with no database")
	}
	if _, err := f.store.Purge(t.Context(), f.event.Account, 1); err == nil {
		t.Fatal("purge succeeded with no database")
	}
}
