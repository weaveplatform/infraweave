package blobs_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

// TestUnitContentKeyRegistered checks that resealing after a rotation
// finds the content key column.
func TestUnitContentKeyRegistered(t *testing.T) {
	for _, c := range crypt.Registry() {
		if c.Table == "blob_objects" && c.Name == "content_key_sealed" &&
			strings.Join(c.Key, ",") == "store,key" {
			return
		}
	}
	t.Error("blob_objects.content_key_sealed(store,key) is not in crypt.Registry")
}

type harness struct {
	pool  *pgxpool.Pool
	store objectstore.Store
	o     *blobs.Objects
	r     *blobs.Reaper
	q     *queue.Queue
	keys  *crypt.Keyring
	// run starts the workers that run blob.delete.
	run func()
}

// open gives each test a database and an fs store of its own. driver
// names the driver Objects takes the store for, so a test can ask for
// gcs's read-back over the fs store.
func open(t *testing.T, driver string) *harness {
	t.Helper()
	db := sqltest.Open(t)
	pool := db.Primary()
	p, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.Load(t.Context(), pool, p)
	if err != nil {
		t.Fatal(err)
	}
	c := objectstore.Defaults()
	c.Driver, c.FS.Root, c.FS.URL = objectstore.DriverFS, t.TempDir(), "http://infraweave.test"
	store, err := objectstore.New(
		c,
		func(string) (objectstore.Bucket, error) { return fs.New(c.FS, keys) },
	)
	if err != nil {
		t.Fatal(err)
	}
	c.Driver = driver
	q := queue.New(pool)
	h := &harness{
		pool:  pool,
		store: store,
		o:     blobs.New(pool, q, objectstore.WriterOnly(store), keys, c),
		r:     blobs.NewReaper(pool, q, store),
		q:     q,
		keys:  keys,
	}
	log := slog.New(slog.DiscardHandler)
	l := notify.NewListener(db.ConnConfig, log)
	jobs, err := l.Subscribe(notify.Jobs)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	wg.Go(func() { l.Run(ctx) })
	cfg := queue.Defaults()
	cfg.PollInterval = 20 * time.Millisecond
	cfg.ShutdownGrace = 5 * time.Second
	h.run = func() {
		reg := queue.NewRegistry()
		if err := h.r.Register(reg); err != nil {
			t.Fatal(err)
		}
		w := queue.NewWorkers(q, reg, cfg, queue.AllowAll{}, jobs.C, log, "test-replica")
		wg.Go(func() { w.Run(ctx) })
	}
	return h
}

// eventually fails the test unless cond holds within 15 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

func (h *harness) exec(t *testing.T, stmt string, args ...any) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), stmt, args...); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

// state is the object's row state, or "" without a row.
func (h *harness) state(t *testing.T, store objectstore.Name, key string) string {
	t.Helper()
	var s string
	err := h.pool.QueryRow(t.Context(), `SELECT state FROM blob_objects WHERE store = $1 AND key = $2`,
		store, key).
		Scan(&s)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
	return s
}

// stored reports whether the bucket holds the object.
func (h *harness) stored(t *testing.T, store objectstore.Name, key string) bool {
	t.Helper()
	_, err := h.store.Head(t.Context(), store, key)
	if err != nil && !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// gone waits until both the row and the object are gone.
func (h *harness) gone(t *testing.T, store objectstore.Name, key string) {
	t.Helper()
	eventually(t, string(store)+"/"+key+" removed", func() bool {
		return h.state(t, store, key) == "" && !h.stored(t, store, key)
	})
}

// put writes body under key and commits it in a transaction of its own.
func (h *harness) put(t *testing.T, store objectstore.Name, key string, body []byte) {
	t.Helper()
	obj, err := h.o.Put(t.Context(), store, key, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if obj.Size != int64(len(body)) || obj.SHA256 != sha256.Sum256(body) {
		t.Fatalf("Put returned %d bytes, SHA-256 %x", obj.Size, obj.SHA256)
	}
	if err := tx.Do(t.Context(), h.pool, tx.Options{}, func(ctx context.Context) error {
		return h.o.Commit(ctx, store, key)
	}); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) read(t *testing.T, store objectstore.Name, key string) []byte {
	t.Helper()
	r, err := h.o.Open(t.Context(), store, key)
	if err != nil {
		t.Fatalf("Open %s/%s: %v", store, key, err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func random(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// TestAccBlobsExpire checks blob.expire: a pending row older than 24
// hours and a live row past expires_at go with their objects, and a fresh
// pending row and an unexpired live row stay.
func TestAccBlobsExpire(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	ctx := t.Context()
	for _, k := range []string{"acct/abandoned", "acct/fresh"} {
		if _, err := h.o.Put(
			ctx,
			objectstore.RunLogs,
			k,
			bytes.NewReader(random(10)),
			10,
		); err != nil {
			t.Fatal(err)
		}
	}
	h.put(t, objectstore.RunLogs, "acct/expired", random(10))
	h.put(t, objectstore.RunLogs, "acct/current", random(10))
	h.put(t, objectstore.States, "acct/forever", random(10))
	h.exec(
		t,
		`UPDATE blob_objects SET created_at = now() - interval '25 hours' WHERE key = 'acct/abandoned'`,
	)
	h.exec(
		t,
		`UPDATE blob_objects SET created_at = now() - interval '23 hours' WHERE key = 'acct/fresh'`,
	)
	h.exec(
		t,
		`UPDATE blob_objects SET expires_at = now() - interval '1 second' WHERE key = 'acct/expired'`,
	)

	var expires *time.Time
	if err := h.pool.QueryRow(ctx, `SELECT expires_at FROM blob_objects WHERE key = 'acct/forever'`).
		Scan(&expires); err != nil ||
		expires != nil {
		t.Fatalf("a state version expires at %v, %v", expires, err)
	}
	if err := h.pool.QueryRow(ctx, `
SELECT expires_at FROM blob_objects WHERE key = 'acct/current'`).Scan(&expires); err != nil ||
		expires == nil || time.Until(*expires) < 59*24*time.Hour {
		t.Fatalf("a run log expires at %v, %v; want in 60 days", expires, err)
	}

	n, err := h.r.Expire(ctx, blobs.ExpireBatch)
	if err != nil || n != 2 {
		t.Fatalf("Expire marked %d, %v; want 2", n, err)
	}
	h.run()
	h.gone(t, objectstore.RunLogs, "acct/abandoned")
	h.gone(t, objectstore.RunLogs, "acct/expired")
	for _, k := range []string{"acct/fresh", "acct/current"} {
		if !h.stored(t, objectstore.RunLogs, k) ||
			h.state(t, objectstore.RunLogs, k) == "deleting" {
			t.Errorf("%s was expired", k)
		}
	}
	if n, err := h.r.Expire(ctx, blobs.ExpireBatch); err != nil || n != 0 {
		t.Errorf("a second Expire marked %d, %v", n, err)
	}
}

// TestAccBlobsExpireBatch checks that one run marks at most its limit.
func TestAccBlobsExpireBatch(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	for _, k := range []string{"acct/a", "acct/b", "acct/c"} {
		h.put(t, objectstore.Uploads, k, nil)
	}
	h.exec(t, `UPDATE blob_objects SET expires_at = now() - interval '1 second'`)
	for _, want := range []int{2, 1, 0} {
		if n, err := h.r.Expire(t.Context(), 2); err != nil || n != want {
			t.Fatalf("Expire marked %d, %v; want %d", n, err, want)
		}
	}
}

// TestAccBlobsDelete checks that a delete in a transaction that rolls
// back leaves the object live, and one that commits removes it after
// the commit.
func TestAccBlobsDelete(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	body := random(100)
	h.put(t, objectstore.RunArtifacts, "acct/plan", body)
	h.run()

	rollback := errors.New("the business change failed")
	err := tx.Do(t.Context(), h.pool, tx.Options{}, func(ctx context.Context) error {
		if err := h.o.Delete(ctx, objectstore.RunArtifacts, "acct/plan"); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if h.state(t, objectstore.RunArtifacts, "acct/plan") != "live" ||
		!bytes.Equal(h.read(t, objectstore.RunArtifacts, "acct/plan"), body) {
		t.Fatal("a rolled-back delete removed the object")
	}

	if err := tx.Do(t.Context(), h.pool, tx.Options{}, func(ctx context.Context) error {
		if err := h.o.Delete(ctx, objectstore.RunArtifacts, "acct/plan"); err != nil {
			return err
		}
		// A second delete in the same change enqueues nothing more.
		return h.o.Delete(ctx, objectstore.RunArtifacts, "acct/plan")
	}); err != nil {
		t.Fatal(err)
	}
	h.gone(t, objectstore.RunArtifacts, "acct/plan")
	if _, err := h.o.Open(
		t.Context(),
		objectstore.RunArtifacts,
		"acct/plan",
	); !errors.Is(
		err,
		objectstore.ErrNotFound,
	) {
		t.Errorf("Open of a deleted object: %v", err)
	}
	if err := h.o.Delete(
		t.Context(),
		objectstore.RunArtifacts,
		"acct/plan",
	); !errors.Is(
		err,
		queue.ErrNoTransaction,
	) {
		t.Errorf("Delete outside a transaction: %v", err)
	}
}

// TestAccBlobsSealed checks that a sealed store's object holds no
// plaintext in the bucket, reads back through Open, and that its content
// key copied to another row does not open.
func TestAccBlobsSealed(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	// The plaintext repeats a marker the sealed bytes must not contain.
	body := bytes.Repeat([]byte("terraform-state-marker "), 5000)
	for _, k := range []string{"acct/a", "acct/b"} {
		h.put(t, objectstore.States, k, body)
	}
	raw, err := h.store.Get(t.Context(), objectstore.States, "acct/a")
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := io.ReadAll(raw)
	raw.Close()
	if bytes.Contains(sealed, []byte("terraform-state-marker")) ||
		int64(len(sealed)) != seal.ObjectSize(int64(len(body))) {
		t.Fatalf("the bucket holds %d bytes with plaintext in them", len(sealed))
	}
	if !bytes.Equal(h.read(t, objectstore.States, "acct/a"), body) {
		t.Fatal("the sealed object did not read back")
	}
	h.exec(t, `
UPDATE blob_objects SET content_key_sealed = (SELECT content_key_sealed FROM blob_objects WHERE key = 'acct/a')
WHERE key = 'acct/b'`)
	if _, err := h.o.Open(t.Context(), objectstore.States, "acct/b"); err == nil {
		t.Error("a content key copied to another row opened")
	}

	for name, src := range map[string]io.Reader{
		"short": bytes.NewReader(random(5)),
		"long":  bytes.NewReader(random(15)),
	} {
		if _, err := h.o.Put(
			t.Context(),
			objectstore.States,
			"acct/"+name,
			src,
			10,
		); !errors.Is(
			err,
			objectstore.ErrSize,
		) {
			t.Errorf("a %s sealed write: %v", name, err)
		}
		if h.state(t, objectstore.States, "acct/"+name) != "pending" {
			t.Errorf("a %s sealed write left no pending row", name)
		}
	}
}

// signed uploads body to key through a signed PUT URL, declaring
// declared's size and SHA-256.
func (h *harness) signed(t *testing.T, key string, declared, body []byte) {
	t.Helper()
	sum := sha256.Sum256(declared)
	if err := h.o.Begin(
		t.Context(),
		objectstore.Uploads,
		key,
		int64(len(declared)),
		sum[:],
	); err != nil {
		t.Fatal(err)
	}
	// The fs PUT handler would refuse a mismatch, so the test writes
	// what a gcs or azureblob URL would accept.
	if _, err := h.store.Put(
		t.Context(),
		objectstore.Uploads,
		key,
		bytes.NewReader(body),
		int64(len(body)),
	); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) commitUpload(t *testing.T, key string) error {
	t.Helper()
	return tx.Do(t.Context(), h.pool, tx.Options{}, func(ctx context.Context) error {
		_, err := h.o.CommitUpload(ctx, objectstore.Uploads, key)
		return err
	})
}

// TestAccBlobsCommitUpload checks a signed upload's commit: the size is
// checked by Head, and on gcs and azureblob the bytes are read back; a
// mismatch leaves the object under its pending row and deletes nothing.
func TestAccBlobsCommitUpload(t *testing.T) {
	h := open(t, objectstore.DriverGCS)
	body := random(50)
	h.signed(t, "acct/good", body, body)
	if err := h.commitUpload(t, "acct/good"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(h.read(t, objectstore.Uploads, "acct/good"), body) {
		t.Fatal("the upload did not read back")
	}

	h.signed(t, "acct/other", body, random(50))
	if err := h.commitUpload(t, "acct/other"); !errors.Is(err, blobs.ErrChecksum) {
		t.Errorf("other bytes of the declared size: %v", err)
	}
	h.signed(t, "acct/longer", body, random(51))
	if err := h.commitUpload(t, "acct/longer"); !errors.Is(err, objectstore.ErrSize) {
		t.Errorf("more bytes than declared: %v", err)
	}
	for _, k := range []string{"acct/other", "acct/longer"} {
		if h.state(t, objectstore.Uploads, k) != "pending" || !h.stored(t, objectstore.Uploads, k) {
			t.Errorf("%s: a failed commit did not leave the object under its pending row", k)
		}
	}

	sum := sha256.Sum256(body)
	if err := h.o.Begin(t.Context(), objectstore.Uploads, "acct/never", 50, sum[:]); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"acct/never", "acct/no-row"} {
		if err := h.commitUpload(t, k); !errors.Is(err, blobs.ErrNotUploaded) {
			t.Errorf("%s: %v", k, err)
		}
	}
}

// TestAccBlobsCommitUploadNoReadBack checks that a driver whose URLs bind
// the SHA-256 commits on Head alone.
func TestAccBlobsCommitUploadNoReadBack(t *testing.T) {
	h := open(t, objectstore.DriverS3)
	h.signed(t, "acct/trusted", random(50), random(50))
	if err := h.commitUpload(t, "acct/trusted"); err != nil {
		t.Fatal(err)
	}
}

// TestAccBlobsRefusals checks what Begin, Put, Commit and Open refuse.
func TestAccBlobsRefusals(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	ctx := t.Context()
	sum := sha256.Sum256(nil)
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"sealed store":  {h.o.Begin(ctx, objectstore.States, "acct/s", 0, sum[:]), blobs.ErrSealed},
		"unknown store": {h.o.Begin(ctx, "elsewhere", "acct/s", 0, sum[:]), objectstore.ErrStore},
		"bad key":       {h.o.Begin(ctx, objectstore.Uploads, "../s", 0, sum[:]), objectstore.ErrKey},
		"short SHA-256": {h.o.Begin(ctx, objectstore.Uploads, "acct/s", 0, sum[:8]), nil},
		"put bad key": {
			func() error { _, err := h.o.Put(ctx, objectstore.Uploads, "a//b", nil, 0); return err }(),
			objectstore.ErrKey,
		},
	} {
		if tc.err == nil || (tc.want != nil && !errors.Is(tc.err, tc.want)) {
			t.Errorf("%s: %v, want %v", name, tc.err, tc.want)
		}
	}
	if err := h.o.Begin(ctx, objectstore.Uploads, "acct/once", 0, sum[:]); err != nil {
		t.Fatal(err)
	}
	if err := h.o.Begin(ctx, objectstore.Uploads, "acct/once", 0, sum[:]); err != nil {
		t.Errorf("a pending upload declared again as it was: %v", err)
	}
	if err := h.o.Begin(ctx, objectstore.Uploads, "acct/once", 1, sum[:]); !errors.Is(
		err,
		blobs.ErrKeyInUse,
	) {
		t.Errorf("a key declared again otherwise: %v", err)
	}
	if _, err := h.o.Put(
		ctx,
		objectstore.Uploads,
		"acct/once",
		bytes.NewReader(nil),
		0,
	); !errors.Is(
		err,
		blobs.ErrKeyInUse,
	) {
		t.Errorf("a Put to a key in use: %v", err)
	}
	if err := h.o.Commit(
		ctx,
		objectstore.Uploads,
		"acct/missing",
	); !errors.Is(
		err,
		blobs.ErrNotUploaded,
	) {
		t.Errorf("Commit with no row: %v", err)
	}
	if _, err := h.o.Put(
		ctx,
		objectstore.Uploads,
		"acct/pending",
		bytes.NewReader(nil),
		0,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := h.o.Open(
		ctx,
		objectstore.Uploads,
		"acct/pending",
	); !errors.Is(
		err,
		objectstore.ErrNotFound,
	) {
		t.Errorf("Open of a pending object: %v", err)
	}
}

// TestAccBlobsSealedUpload checks an upload its uploader sealed: the row
// holds the content key the uploader sealed with, sealed in turn, so the
// committed object opens to the plaintext; a store that is not sealed and
// a content key of the wrong size are refused, a commit answers the
// declared size and SHA-256, and a committed key is not declared again.
func TestAccBlobsSealedUpload(t *testing.T) {
	h := open(t, objectstore.DriverFS)
	ctx := t.Context()
	content := bytes.Repeat([]byte{9}, seal.KeySize)
	body := bytes.Repeat([]byte("plan-json-marker "), 9000)
	var sealed bytes.Buffer
	sw, err := seal.NewWriter(&sealed, content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := sw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(sealed.Bytes())
	size := int64(sealed.Len())
	const key = "acct/run/PLANNING-4/plan.json"
	for name, err := range map[string]error{
		"plain store": h.o.BeginSealed(ctx, objectstore.Uploads, "acct/p", size, sum[:], content),
		"short key":   h.o.BeginSealed(ctx, objectstore.RunArtifacts, key, size, sum[:], content[:16]),
		"bad size":    h.o.BeginSealed(ctx, objectstore.RunArtifacts, key, -1, sum[:], content),
	} {
		want := blobs.ErrDeclared
		if name == "plain store" {
			want = blobs.ErrNotSealed
		}
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
	}
	for range 2 {
		if err := h.o.BeginSealed(ctx, objectstore.RunArtifacts, key, size, sum[:],
			content); err != nil {
			t.Fatal(err)
		}
	}
	signed, err := h.o.SignPut(ctx, objectstore.RunArtifacts, key, size, sum[:])
	if err != nil || !strings.HasPrefix(signed.URL, "http://infraweave.test/blobs/") {
		t.Fatalf("SignPut = %+v, %v", signed, err)
	}
	if _, err := h.o.SignGet(ctx, objectstore.RunArtifacts, key); !errors.Is(
		err, objectstore.ErrNotFound) {
		t.Errorf("SignGet of a pending object: %v", err)
	}
	if _, err := h.store.Put(ctx, objectstore.RunArtifacts, key, bytes.NewReader(sealed.Bytes()),
		size); err != nil {
		t.Fatal(err)
	}
	var obj objectstore.Object
	if err := tx.Do(ctx, h.pool, tx.Options{}, func(ctx context.Context) error {
		obj, err = h.o.CommitUpload(ctx, objectstore.RunArtifacts, key)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if obj.Size != size || obj.SHA256 != sum {
		t.Errorf("CommitUpload = %+v", obj)
	}
	if !bytes.Equal(h.read(t, objectstore.RunArtifacts, key), body) {
		t.Error("the uploader's sealed object did not open")
	}
	if got, err := h.o.SignGet(ctx, objectstore.RunArtifacts, key); err != nil ||
		!strings.HasPrefix(got.URL, "http://infraweave.test/blobs/") {
		t.Errorf("SignGet = %+v, %v", got, err)
	}
	if err := h.o.BeginSealed(ctx, objectstore.RunArtifacts, key, size, sum[:],
		content); !errors.Is(err, blobs.ErrKeyInUse) {
		t.Errorf("a live key declared again: %v", err)
	}
	h.pool.Close()
	if err := h.o.BeginSealed(ctx, objectstore.RunArtifacts, key, size, sum[:],
		content); err == nil || errors.Is(err, blobs.ErrKeyInUse) {
		t.Errorf("BeginSealed on a closed pool: %v", err)
	}
	if _, err := h.o.SignGet(ctx, objectstore.RunArtifacts, key); err == nil {
		t.Error("SignGet on a closed pool succeeded")
	}
}
