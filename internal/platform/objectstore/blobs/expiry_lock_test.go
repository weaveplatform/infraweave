package blobs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccBlobsExpiryAwareLock(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	ctx := t.Context()
	const key = "acct/retained"
	body := []byte("stored delivery body")
	if _, err := h.o.Put(ctx, objectstore.Inbox, key, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	lock := func(at time.Time) (objectstore.Object, error) {
		var obj objectstore.Object
		err := tx.Do(ctx, h.pool, tx.Options{}, func(inner context.Context) error {
			var err error
			obj, err = h.o.LockLiveAt(inner, objectstore.Inbox, key, at)
			return err
		})
		return obj, err
	}
	if _, err := h.o.LockLiveAt(ctx, objectstore.Inbox, key, at); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal("expiry lock ran without a transaction", err)
	}
	if _, err := lock(at); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("pending body became available", err)
	}
	if err := tx.Do(ctx, h.pool, tx.Options{}, func(inner context.Context) error {
		return h.o.Commit(inner, objectstore.Inbox, key)
	}); err != nil {
		t.Fatal(err)
	}
	h.exec(t, `UPDATE blob_objects SET expires_at=$1 WHERE store='inbox' AND key=$2`, at, key)
	if obj, err := lock(at.Add(-time.Microsecond)); err != nil || obj.Size != int64(len(body)) || obj.SHA256 != sha256.Sum256(body) {
		t.Fatal("live body lost its plaintext identity", obj, err)
	}
	for _, when := range []time.Time{at, at.Add(time.Microsecond)} {
		if _, err := lock(when); !errors.Is(err, objectstore.ErrNotFound) {
			t.Fatal("elapsed unswept body was accepted", when, err)
		}
	}
	if h.state(t, objectstore.Inbox, key) != "live" || !h.stored(t, objectstore.Inbox, key) {
		t.Fatal("expiry refusal depended on the sweep deleting the body")
	}
	h.exec(t, `UPDATE blob_objects SET expires_at=NULL WHERE store='inbox' AND key=$1`, key)
	if _, err := lock(at.Add(24 * time.Hour)); err != nil {
		t.Fatal("nonexpiring object was refused", err)
	}
	broken := tx.Do(ctx, failingDB{}, tx.Options{}, func(inner context.Context) error {
		_, err := h.o.LockLiveAt(inner, objectstore.Inbox, key, at)
		return err
	})
	if !errors.Is(broken, errQuery) || errors.Is(broken, objectstore.ErrNotFound) {
		t.Fatal("database failure became an expired body", broken)
	}
	other, err := pgxpool.NewWithConfig(ctx, h.pool.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := tx.Do(ctx, h.pool, tx.Options{}, func(inner context.Context) error {
		if _, err := h.o.LockLiveAt(inner, objectstore.Inbox, key, at); err != nil {
			return err
		}
		//nolint:contextcheck // a separate transaction must contend with the object's held row
		blocked := tx.Do(ctx, other, tx.Options{}, func(contender context.Context) error {
			if _, err := tx.Q(contender, other).Exec(contender, `SET LOCAL lock_timeout='50ms'`); err != nil {
				return err
			}
			_, err := tx.Q(contender, other).Exec(contender, `UPDATE blob_objects SET state='deleting'
				WHERE store='inbox' AND key=$1`, key)
			return err
		})
		if cause, ok := errors.AsType[*pgconn.PgError](blocked); !ok || cause.Code != "55P03" {
			return errors.New("expiry-aware consumption did not hold off deletion")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(ctx, h.pool, tx.Options{}, func(inner context.Context) error {
		return h.o.Delete(inner, objectstore.Inbox, key)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := lock(at); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("deleting body was accepted", err)
	}
}
