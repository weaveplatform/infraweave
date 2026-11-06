package blobs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var errBucket = errors.New("the bucket is down")

// broken is a store whose every call fails, or deletes with del.
type broken struct{ del error }

func (broken) Put(
	context.Context,
	objectstore.Name,
	string,
	io.Reader,
	int64,
) (objectstore.Object, error) {
	return objectstore.Object{}, errBucket
}

func (broken) Get(context.Context, objectstore.Name, string) (io.ReadCloser, error) {
	return nil, errBucket
}

func (broken) Head(context.Context, objectstore.Name, string) (int64, error) { return 0, errBucket }

func (b broken) Delete(context.Context, objectstore.Name, string) error { return b.del }

func (broken) SignURL(
	context.Context, string, objectstore.Name, string, time.Duration, objectstore.Constraints,
) (objectstore.Signed, error) {
	return objectstore.Signed{}, errBucket
}

func job(t *testing.T, payload any) queue.Job {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return queue.Job{Kind: blobs.DeleteKind.Name, Payload: b}
}

// TestUnitBlobsUnreachable checks that every call on a server that
// refuses connections reports kernel.ErrUnavailable.
func TestUnitBlobsUnreachable(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://iw:iw@127.0.0.1:1/iw?connect_timeout=5")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := queue.New(pool)
	o := blobs.New(pool, q, broken{}, nil, objectstore.Defaults())
	r := blobs.NewReaper(pool, q, broken{})
	ctx := t.Context()
	sum := make([]byte, 32)
	for name, call := range map[string]func() error{
		"Begin":  func() error { return o.Begin(ctx, objectstore.Uploads, "acct/k", 0, sum) },
		"Put":    func() error { _, err := o.Put(ctx, objectstore.Uploads, "acct/k", bytes.NewReader(nil), 0); return err },
		"Commit": func() error { return o.Commit(ctx, objectstore.Uploads, "acct/k") },
		"CommitUpload": func() error {
			return o.CommitUpload(ctx, objectstore.Uploads, "acct/k")
		},
		"Open":   func() error { _, err := o.Open(ctx, objectstore.Uploads, "acct/k"); return err },
		"Expire": func() error { _, err := r.Expire(ctx, 10); return err },
		"remove": func() error {
			return r.Remove(ctx, job(t, map[string]string{"store": "uploads", "key": "acct/k"}))
		},
	} {
		if err := call(); !errors.Is(err, kernel.ErrUnavailable) {
			t.Errorf("%s: %v, want kernel.ErrUnavailable", name, err)
		}
	}
}

// TestUnitBlobsRemoveErrors checks blob.delete's errors before it
// reaches the database: a malformed payload, a refused key and a failing
// bucket.
func TestUnitBlobsRemoveErrors(t *testing.T) {
	ctx := t.Context()
	if err := blobs.NewReaper(nil, nil, broken{}).
		Remove(ctx, queue.Job{Payload: []byte("{")}); err == nil {
		t.Error("a malformed payload succeeded")
	}
	for _, want := range []error{objectstore.ErrKey, objectstore.ErrStore, errBucket} {
		r := blobs.NewReaper(nil, nil, broken{del: want})
		if err := r.Remove(
			ctx,
			job(t, map[string]string{"store": "uploads", "key": "acct/k"}),
		); !errors.Is(
			err,
			want,
		) {
			t.Errorf("a bucket failing with %v: %v", want, err)
		}
	}
	reg := queue.NewRegistry()
	r := blobs.NewReaper(nil, nil, broken{})
	if err := r.Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(reg); err == nil {
		t.Error("registering blob.delete twice succeeded")
	}
}

// TestAccBlobsDeleteGivesUp checks that blob.delete dies at its first
// attempt on a payload, store or key that can never succeed.
func TestAccBlobsDeleteGivesUp(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	h.run()
	for name, payload := range map[string]any{
		"payload": "not an object",
		"store":   map[string]string{"store": "elsewhere", "key": "acct/k"},
		"key":     map[string]string{"store": "uploads", "key": "a//b"},
	} {
		id, err := h.q.Enqueue(t.Context(), blobs.DeleteKind, payload, queue.UniqueKey(name))
		if err != nil {
			t.Fatal(err)
		}
		eventually(t, name+" dead", func() bool {
			var state string
			var attempt int
			if err := h.pool.QueryRow(t.Context(), `SELECT state, attempt FROM jobs WHERE id = $1`, id).
				Scan(&state, &attempt); err != nil {
				t.Fatal(err)
			}
			if state == "dead" && attempt != 1 {
				t.Fatalf("%s died at attempt %d", name, attempt)
			}
			return state == "dead"
		})
	}
}

// TestAccBlobsBrokenBucket checks that a write the bucket fails leaves
// its pending row for blob.expire, and that reads and commits report the
// bucket's error.
func TestAccBlobsBrokenBucket(t *testing.T) {
	h := open(t, objectstore.DriverGCS)
	c := objectstore.Defaults()
	c.Driver = objectstore.DriverGCS
	o := blobs.New(h.pool, h.q, broken{}, h.keys, c)
	ctx := t.Context()
	for _, store := range []objectstore.Name{objectstore.Uploads, objectstore.States} {
		if _, err := o.Put(
			ctx,
			store,
			"acct/k",
			bytes.NewReader(make([]byte, 10)),
			10,
		); !errors.Is(
			err,
			errBucket,
		) {
			t.Errorf("%s: Put: %v", store, err)
		}
		if h.state(t, store, "acct/k") != "pending" {
			t.Errorf("%s: a failed write left no pending row", store)
		}
	}

	h.put(t, objectstore.Uploads, "acct/live", []byte("x"))
	if _, err := o.Open(ctx, objectstore.Uploads, "acct/live"); !errors.Is(err, errBucket) {
		t.Errorf("Open: %v", err)
	}
	h.signed(t, "acct/upload", []byte("y"), []byte("y"))
	commit := func(o *blobs.Objects) error {
		return tx.Do(ctx, h.pool, tx.Options{}, func(ctx context.Context) error {
			return o.CommitUpload(ctx, objectstore.Uploads, "acct/upload")
		})
	}
	if err := commit(o); !errors.Is(err, errBucket) {
		t.Errorf("CommitUpload on a failing Head: %v", err)
	}
	// Head answers but Get fails: the read-back reports it.
	if err := commit(
		blobs.New(h.pool, h.q, noGet{objectstore.WriterOnly(h.store)}, h.keys, c),
	); !errors.Is(
		err,
		errBucket,
	) {
		t.Errorf("CommitUpload on a failing read-back: %v", err)
	}
	if h.state(t, objectstore.Uploads, "acct/upload") != "pending" {
		t.Error("a failed commit did not leave the row pending")
	}
}

// noGet is a writer whose Get fails.
type noGet struct{ objectstore.Writer }

func (noGet) Get(context.Context, objectstore.Name, string) (io.ReadCloser, error) {
	return nil, errBucket
}
