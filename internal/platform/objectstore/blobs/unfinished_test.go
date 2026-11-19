package blobs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

type heldUpload struct {
	io.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *heldUpload) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.Reader.Read(p)
}

// Deletion during a real upload must retain the tombstone until the writer
// finishes, then remove both body and row. Otherwise the writer can recreate
// an object after its deletion job has forgotten it.
func TestAccBlobsDeleteUnfinished(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	ctx := t.Context()
	const key = "acct/unfinished"
	body := []byte("the compacted log")
	r := &heldUpload{Reader: bytes.NewReader(body), started: make(chan struct{}), release: make(chan struct{})}
	finished := make(chan struct{})
	var uploadErr error
	go func() {
		defer close(finished)
		_, uploadErr = h.o.Put(ctx, objectstore.RunLogs, key, r, int64(len(body)))
	}()
	var release sync.Once
	t.Cleanup(func() {
		release.Do(func() { close(r.release) })
		<-finished
	})
	<-r.started
	if err := tx.Do(ctx, h.pool, tx.Options{}, func(ctx context.Context) error {
		return h.o.Delete(ctx, objectstore.RunLogs, key)
	}); err != nil {
		t.Fatal("delete before the upload checksum exists", err)
	}
	h.run()
	eventually(t, "deletion deferred for the active writer", func() bool {
		var deferred bool
		err := h.pool.QueryRow(ctx, `SELECT generation > 0 AND state = 'pending' AND attempt = 0
FROM jobs WHERE kind = 'blob.delete'`).Scan(&deferred)
		if err != nil {
			t.Fatal(err)
		}
		return deferred
	})
	if h.state(t, objectstore.RunLogs, key) != "deleting" {
		t.Fatal("the active writer lost its tombstone")
	}
	release.Do(func() { close(r.release) })
	<-finished
	if uploadErr != nil {
		t.Fatal(uploadErr)
	}
	if err := tx.Do(ctx, h.pool, tx.Options{}, func(ctx context.Context) error {
		return h.o.Commit(ctx, objectstore.RunLogs, key)
	}); !errors.Is(err, blobs.ErrNotUploaded) {
		t.Fatal("an upload marked for deletion became live", err)
	}
	h.gone(t, objectstore.RunLogs, key)
}

func TestAccBlobsExpireUnfinished(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	ctx := t.Context()
	const key = "acct/failed-upload"
	if _, err := h.o.Put(ctx, objectstore.RunLogs, key, bytes.NewReader([]byte("short")), 100); err == nil {
		t.Fatal("short upload succeeded")
	}
	if _, err := h.pool.Exec(ctx, `UPDATE blob_objects SET state = 'live' WHERE key = $1`, key); err == nil {
		t.Fatal("an unfinished upload became live without a checksum")
	}
	h.exec(t, `UPDATE blob_objects SET created_at = now() - interval '25 hours' WHERE key = $1`, key)
	if n, err := h.r.Expire(ctx, blobs.ExpireBatch); err != nil || n != 1 {
		t.Fatalf("expire unfinished upload = %d, %v", n, err)
	}
	h.run()
	h.gone(t, objectstore.RunLogs, key)
}
