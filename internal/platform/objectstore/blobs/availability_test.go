package blobs_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccBlobsAvailabilityBatchUsesExactStoreLifecycleAndExpiry(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	at := time.Now().UTC().Truncate(time.Microsecond)
	put := func(store objectstore.Name, key string, publish bool) {
		t.Helper()
		body := []byte("private batch payload")
		if _, err := h.o.Put(t.Context(), store, key, bytes.NewReader(body), int64(len(body))); err != nil {
			t.Fatal(err)
		}
		if publish {
			if err := tx.Do(t.Context(), h.pool, tx.Options{}, func(ctx context.Context) error { return h.o.Commit(ctx, store, key) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, key := range []string{"acct/live", "acct/expired", "acct/deleting"} {
		put(objectstore.PolicyInputs, key, true)
	}
	put(objectstore.PolicyInputs, "acct/pending", false)
	put(objectstore.Inbox, "acct/other-store", true)
	h.exec(t, `UPDATE blob_objects SET expires_at=$1 WHERE store='policy-inputs' AND key='acct/expired'`, at)
	if err := tx.Do(t.Context(), h.pool, tx.Options{}, func(ctx context.Context) error { return h.o.Delete(ctx, objectstore.PolicyInputs, "acct/deleting") }); err != nil {
		t.Fatal(err)
	}
	keys := []string{"acct/live", "acct/expired", "acct/deleting", "acct/pending", "acct/other-store", "acct/missing", "acct/live"}
	got, err := h.o.AvailableBatch(t.Context(), objectstore.PolicyInputs, keys, at)
	if err != nil || len(got) != 1 || !got["acct/live"] {
		t.Fatalf("owned batch availability: %v/%v", got, err)
	}
	before, err := h.o.AvailableBatch(t.Context(), objectstore.PolicyInputs, keys, at.Add(-time.Microsecond))
	if err != nil || len(before) != 2 || !before["acct/expired"] || !before["acct/live"] {
		t.Fatalf("strict elapsed ceiling: %v/%v", before, err)
	}
	if got, err := h.o.AvailableBatch(t.Context(), objectstore.PolicyInputs, nil, at); err != nil || len(got) != 0 {
		t.Fatalf("empty batch: %v/%v", got, err)
	}
	if _, err := h.o.AvailableBatch(t.Context(), objectstore.PolicyInputs, make([]string, 501), at); !errors.Is(err, objectstore.ErrSize) {
		t.Fatalf("unbounded batch accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.o.AvailableBatch(ctx, objectstore.PolicyInputs, keys, at); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled metadata read: %v", err)
	}
}
