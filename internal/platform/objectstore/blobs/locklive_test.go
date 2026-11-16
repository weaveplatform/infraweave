package blobs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// TestAccBlobsLockLive checks that LockLive answers a live sealed
// object's plaintext size and SHA-256 in a transaction, refuses to run
// outside one, finds no pending object, and holds the row so a second
// caller waits for the first to delete the object and then finds it
// gone.
func TestAccBlobsLockLive(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	ctx := t.Context()
	body := []byte(`{"version":4,"serial":1,"lineage":"l"}`)
	if _, err := h.o.PutSealed(ctx, objectstore.Uploads, "acct/live", bytes.NewReader(body),
		int64(len(body))); err != nil {
		t.Fatal(err)
	}
	if _, err := h.o.LockLive(ctx, objectstore.Uploads, "acct/live"); !errors.Is(err,
		queue.ErrNoTransaction) {
		t.Errorf("LockLive outside a transaction = %v", err)
	}
	lock := func(ctx context.Context) (objectstore.Object, error) {
		var obj objectstore.Object
		err := tx.Do(ctx, h.pool, tx.Options{}, func(ctx context.Context) error {
			var err error
			obj, err = h.o.LockLive(ctx, objectstore.Uploads, "acct/live")
			return err
		})
		return obj, err
	}
	if _, err := lock(ctx); !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("LockLive of a pending object = %v", err)
	}
	if err := tx.Do(ctx, h.pool, tx.Options{}, func(ctx context.Context) error {
		return h.o.Commit(ctx, objectstore.Uploads, "acct/live")
	}); err != nil {
		t.Fatal(err)
	}
	if obj, err := lock(ctx); err != nil || obj.Size != int64(len(body)) ||
		obj.SHA256 != sha256.Sum256(body) {
		t.Fatalf("LockLive = %+v, %v", obj, err)
	}

	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- tx.Do(context.WithoutCancel(ctx), h.pool, tx.Options{},
			func(ctx context.Context) error {
				if _, err := h.o.LockLive(ctx, objectstore.Uploads, "acct/live"); err != nil {
					return err
				}
				if err := h.o.Delete(ctx, objectstore.Uploads, "acct/live"); err != nil {
					return err
				}
				close(locked)
				<-release
				return nil
			})
	}()
	<-locked
	second := make(chan error, 1)
	go func() {
		_, err := lock(context.WithoutCancel(ctx))
		second <- err
	}()
	awaitLockWait(t, h)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-second; !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("a second LockLive after the first deleted the object = %v", err)
	}

	broken := tx.Do(ctx, failingDB{}, tx.Options{}, func(ctx context.Context) error {
		_, err := h.o.LockLive(ctx, objectstore.Uploads, "acct/live")
		return err
	})
	if broken == nil || errors.Is(broken, objectstore.ErrNotFound) {
		t.Errorf("LockLive on a failing transaction = %v", broken)
	}
}

// awaitLockWait returns once a session of h's database waits on a lock.
func awaitLockWait(t *testing.T, h *harness) {
	t.Helper()
	for t.Context().Err() == nil {
		var n int
		if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
	}
	t.Fatal("no session waited on a lock")
}

// failingDB begins transactions whose every query fails.
type (
	failingDB struct{}
	failingTx struct{ pgx.Tx }
	failedRow struct{}
)

var errQuery = errors.New("the query failed")

func (failingDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return failingTx{}, nil }
func (failingTx) Commit(context.Context) error                           { return nil }
func (failingTx) Rollback(context.Context) error                         { return nil }
func (failingTx) QueryRow(context.Context, string, ...any) pgx.Row       { return failedRow{} }
func (failedRow) Scan(...any) error                                      { return errQuery }
