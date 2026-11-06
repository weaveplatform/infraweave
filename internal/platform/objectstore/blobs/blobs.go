package blobs

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// The jobs that remove objects, on the maintenance queue with policy
// Ignore: blob.delete for one object, keyed by its store and key, and the
// periodic blob.expire.
var (
	DeleteKind = queue.Kind{Name: "blob.delete", Queue: queue.Maintenance, Policy: queue.Ignore}
	ExpireKind = queue.Kind{Name: "blob.expire", Queue: queue.Maintenance, Policy: queue.Ignore}
)

// Decision 0005's expiry.
const (
	// ExpireEvery is blob.expire's interval.
	ExpireEvery = 10 * time.Minute
	// ExpireBatch is how many objects one blob.expire run removes.
	ExpireBatch = 1000
	// Abandoned is the age past which a pending row's upload is taken to
	// have failed or rolled back.
	Abandoned = 24 * time.Hour
)

// Errors of recording objects.
var (
	// ErrNotUploaded reports a commit of an object that has no pending
	// row, or whose upload has not finished.
	ErrNotUploaded = errors.New("object is not uploaded")
	// ErrChecksum reports an upload whose bytes do not match the SHA-256
	// its row declares.
	ErrChecksum = errors.New("object does not match its declared SHA-256")
	// ErrSealed reports a signed upload to a sealed store, whose objects
	// the server seals as it writes them.
	ErrSealed = errors.New("a sealed store takes no signed upload")
	// ErrDeclared reports a signed upload declared with a negative size or
	// a SHA-256 of other than 32 bytes.
	ErrDeclared = errors.New("invalid declared size or SHA-256")
	// ErrKeyInUse reports a key that already has an object; an object is
	// written once.
	ErrKeyInUse = errors.New("object key is in use")
)

// contentKey is the column a sealed object's content key is sealed into,
// as crypt.Registry lists it.
var contentKey = crypt.Column{
	Table: "blob_objects",
	Name:  "content_key_sealed",
	Key:   []string{"store", "key"},
}

// Objects records objects in blob_objects as serve writes and reads them,
// through an objectstore.Writer, so it never deletes one.
type Objects struct {
	db        tx.Querier
	q         *queue.Queue
	w         objectstore.Writer
	keys      *crypt.Keyring
	retention objectstore.Retention
	// readBack is set for drivers whose signed PUT URLs bind no SHA-256,
	// so CommitUpload reads the object back to compare.
	readBack bool
}

// New records objects in db, the primary pool, writing them through w and
// sealing a sealed store's under keys. c gives the retention and the
// driver.
func New(
	db tx.Querier,
	q *queue.Queue,
	w objectstore.Writer,
	keys *crypt.Keyring,
	c objectstore.Config,
) *Objects {
	return &Objects{
		db:        db,
		q:         q,
		w:         w,
		keys:      keys,
		retention: c.Retention,
		readBack:  c.Driver == objectstore.DriverGCS || c.Driver == objectstore.DriverAzureBlob,
	}
}

// inUse reports a unique_violation: the key already has a row.
func inUse(err error) bool {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pe.Code == "23505"
}

func check(store objectstore.Name, key string) error {
	if !slices.Contains(objectstore.Names(), store) {
		return fmt.Errorf("%w: %q", objectstore.ErrStore, store)
	}
	return objectstore.CheckKey(key) //nolint:wrapcheck // names the key
}

// begin inserts a pending row on the pool, outside any transaction on
// ctx, so the row outlives a caller's rollback for blob.expire to find.
const begin = `
INSERT INTO blob_objects (store, key, size, sha256, state, content_key_sealed, expires_at)
VALUES ($1, $2, $3, $4, 'pending', $5, now() + $6::interval)`

func (o *Objects) begin(
	ctx context.Context,
	store objectstore.Name,
	key string,
	size int64,
	sum, sealed []byte,
) error {
	var expires *time.Duration
	if d := o.retention.Of(store); d > 0 {
		expires = &d
	}
	if _, err := o.db.Exec(ctx, begin, store, key, size, sum, sealed, expires); err != nil {
		if inUse(err) {
			return fmt.Errorf("%w: %s/%s", ErrKeyInUse, store, key)
		}
		return fmt.Errorf("blobs: begin %s/%s: %w", store, key, sql.Unavailable(err))
	}
	return nil
}

// Begin records a pending object of size bytes and SHA-256 sum before a
// signed PUT URL for it is issued. CommitUpload makes it live.
func (o *Objects) Begin(
	ctx context.Context,
	store objectstore.Name,
	key string,
	size int64,
	sum []byte,
) error {
	if err := check(store, key); err != nil {
		return err
	}
	if store.Sealed() {
		return fmt.Errorf("%w: %s", ErrSealed, store)
	}
	if size < 0 || len(sum) != sha256.Size {
		return fmt.Errorf(
			"%w: %s/%s: size %d, %d-byte SHA-256",
			ErrDeclared,
			store,
			key,
			size,
			len(sum),
		)
	}
	return o.begin(ctx, store, key, size, sum, nil)
}

// uploaded records the SHA-256 of a server-side write once its upload
// ends, on the pool like begin.
const uploaded = `UPDATE blob_objects SET sha256 = $3 WHERE store = $1 AND key = $2 AND state = 'pending'`

// Put records a pending object and writes size bytes of r under it,
// sealed under a content key of its own when the store is sealed, and
// returns the plaintext's size and SHA-256. Commit makes it live. A write
// that fails leaves the pending row and whatever the driver kept for
// blob.expire, since serve cannot delete.
func (o *Objects) Put(
	ctx context.Context,
	store objectstore.Name,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	if err := check(store, key); err != nil {
		return objectstore.Object{}, err
	}
	var (
		obj objectstore.Object
		err error
	)
	if store.Sealed() {
		obj, err = o.putSealed(ctx, store, key, r, size)
	} else if err = o.begin(ctx, store, key, size, nil, nil); err == nil {
		obj, err = o.w.Put(ctx, store, key, r, size)
	}
	if err != nil {
		return objectstore.Object{}, err //nolint:wrapcheck // the driver's errors name the key
	}
	if _, err := o.db.Exec(ctx, uploaded, store, key, obj.SHA256[:]); err != nil {
		return objectstore.Object{}, fmt.Errorf(
			"blobs: record %s/%s: %w",
			store,
			key,
			sql.Unavailable(err),
		)
	}
	return obj, nil
}

// putSealed seals r into a pipe the driver reads, declaring the sealed
// size up front. The header SealObject writes waits in a buffer, so the
// content key is sealed and recorded before the upload starts.
func (o *Objects) putSealed(
	ctx context.Context,
	store objectstore.Name,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	aad, err := contentKey.AAD([]string{string(store), key})
	if err != nil {
		return objectstore.Object{}, err //nolint:wrapcheck // cannot fail: two parts
	}
	pr, pw := io.Pipe()
	bw := bufio.NewWriter(pw)
	sw, sealed, err := o.keys.SealObject(ctx, bw, aad)
	if err != nil {
		return objectstore.Object{}, fmt.Errorf("blobs: seal %s/%s: %w", store, key, err)
	}
	if err := o.begin(ctx, store, key, size, nil, sealed); err != nil {
		return objectstore.Object{}, err
	}
	h := sha256.New()
	done := make(chan error, 1)
	go func() {
		err := sealInto(sw, bw, io.TeeReader(r, h), size)
		pw.CloseWithError(err)
		done <- err
	}()
	_, err = o.w.Put(ctx, store, key, pr, seal.ObjectSize(size))
	// A driver that stops reading early must not leave the sealer blocked.
	pr.CloseWithError(errors.Join(err, io.ErrClosedPipe))
	if serr := <-done; serr != nil && !errors.Is(serr, io.ErrClosedPipe) {
		// The plaintext's own failure, such as a short source, explains
		// the driver's.
		return objectstore.Object{}, fmt.Errorf("blobs: put %s/%s: %w", store, key, serr)
	}
	if err != nil {
		return objectstore.Object{}, err //nolint:wrapcheck // the driver's errors name the key
	}
	obj := objectstore.Object{Size: size}
	h.Sum(obj.SHA256[:0])
	return obj, nil
}

// sealInto seals exactly size bytes of r through sw and flushes bw.
func sealInto(sw *seal.Writer, bw *bufio.Writer, r io.Reader, size int64) error {
	n, err := io.CopyN(sw, r, size)
	switch {
	case errors.Is(err, io.EOF):
		return fmt.Errorf("%w: got %d bytes, want %d", objectstore.ErrSize, n, size)
	case err != nil:
		return err //nolint:wrapcheck // the source's or the pipe's
	}
	if err := objectstore.AtEnd(r); err != nil {
		return err //nolint:wrapcheck // names the size
	}
	if err := sw.Close(); err != nil {
		return err //nolint:wrapcheck // as above
	}
	return bw.Flush() //nolint:wrapcheck // as above
}

// commit makes an uploaded pending row live in the transaction on ctx.
const commit = `
UPDATE blob_objects SET state = 'live'
WHERE store = $1 AND key = $2 AND state = 'pending' AND sha256 IS NOT NULL`

// Commit makes an object Put wrote live, in the transaction on ctx that
// records the business change, so the object is live exactly when the
// change commits.
func (o *Objects) Commit(ctx context.Context, store objectstore.Name, key string) error {
	tag, err := tx.Q(ctx, o.db).Exec(ctx, commit, store, key)
	if err != nil {
		return fmt.Errorf("blobs: commit %s/%s: %w", store, key, sql.Unavailable(err))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s/%s", ErrNotUploaded, store, key)
	}
	return nil
}

const pending = `
SELECT size, sha256 FROM blob_objects
WHERE store = $1 AND key = $2 AND state = 'pending'
FOR UPDATE`

// CommitUpload makes an object a signed PUT uploaded live, in the
// transaction on ctx, once Head shows the declared size and, on gcs and
// azureblob, whose URLs bind no SHA-256, the object read back matches the
// declared SHA-256. A mismatch leaves the row pending for blob.expire.
func (o *Objects) CommitUpload(ctx context.Context, store objectstore.Name, key string) error {
	var (
		size int64
		sum  []byte
	)
	if err := tx.Q(ctx, o.db).QueryRow(ctx, pending, store, key).Scan(&size, &sum); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s/%s", ErrNotUploaded, store, key)
		}
		return fmt.Errorf("blobs: commit %s/%s: %w", store, key, sql.Unavailable(err))
	}
	got, err := o.w.Head(ctx, store, key)
	switch {
	case errors.Is(err, objectstore.ErrNotFound):
		return fmt.Errorf("%w: %s/%s", ErrNotUploaded, store, key)
	case err != nil:
		return err //nolint:wrapcheck // the driver's errors name the key
	case got != size:
		return fmt.Errorf(
			"%w: %s/%s holds %d bytes, want %d",
			objectstore.ErrSize,
			store,
			key,
			got,
			size,
		)
	}
	if o.readBack {
		if err := o.compare(ctx, store, key, sum); err != nil {
			return err
		}
	}
	return o.Commit(ctx, store, key)
}

func (o *Objects) compare(
	ctx context.Context,
	store objectstore.Name,
	key string,
	sum []byte,
) error {
	r, err := o.w.Get(ctx, store, key)
	if err != nil {
		return err //nolint:wrapcheck // the driver's errors name the key
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return fmt.Errorf("blobs: read back %s/%s: %w", store, key, err)
	}
	if !bytes.Equal(h.Sum(nil), sum) {
		return fmt.Errorf("%w: %s/%s", ErrChecksum, store, key)
	}
	return nil
}

const live = `SELECT content_key_sealed FROM blob_objects WHERE store = $1 AND key = $2 AND state = 'live'`

// Open streams a live object, opening a sealed store's object under its
// content key. A key with no live row is objectstore.ErrNotFound.
func (o *Objects) Open(
	ctx context.Context,
	store objectstore.Name,
	key string,
) (io.ReadCloser, error) {
	var sealed []byte
	if err := tx.Q(ctx, o.db).QueryRow(ctx, live, store, key).Scan(&sealed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, objectstore.ErrNotFound
		}
		return nil, fmt.Errorf("blobs: open %s/%s: %w", store, key, sql.Unavailable(err))
	}
	r, err := o.w.Get(ctx, store, key)
	if err != nil || sealed == nil {
		return r, err //nolint:wrapcheck // the driver's errors name the key
	}
	aad, err := contentKey.AAD([]string{string(store), key})
	if err == nil {
		var sr *seal.Reader
		if sr, err = o.keys.OpenObject(ctx, r, aad, sealed); err == nil {
			return struct {
				io.Reader
				io.Closer
			}{sr, r}, nil
		}
	}
	_ = r.Close()
	return nil, fmt.Errorf("blobs: open %s/%s: %w", store, key, err)
}

// target is blob.delete's payload.
type target struct {
	Store objectstore.Name `json:"store"`
	Key   string           `json:"key"`
}

const drop = `UPDATE blob_objects SET state = 'deleting' WHERE store = $1 AND key = $2 AND state <> 'deleting'`

// Delete marks an object deleting and enqueues blob.delete, in the
// transaction on ctx that drops the record referencing it, so the object
// goes after that change commits and stays live if it rolls back.
func (o *Objects) Delete(ctx context.Context, store objectstore.Name, key string) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	tag, err := tx.Q(ctx, o.db).Exec(ctx, drop, store, key)
	if err != nil {
		return fmt.Errorf("blobs: delete %s/%s: %w", store, key, sql.Unavailable(err))
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	_, err = o.q.EnqueueTx(
		ctx,
		DeleteKind,
		target{store, key},
		queue.UniqueKey(string(store)+"/"+key),
	)
	return err //nolint:wrapcheck // EnqueueTx names the kind
}

// Reaper removes objects for the scheduler role, which alone holds the
// full objectstore.Store.
type Reaper struct {
	db queue.DB
	q  *queue.Queue
	s  objectstore.Store
}

// NewReaper removes objects from s and their rows from db, the primary
// pool.
func NewReaper(db queue.DB, q *queue.Queue, s objectstore.Store) *Reaper {
	return &Reaper{db: db, q: q, s: s}
}

// Register registers blob.delete's handler.
func (r *Reaper) Register(reg *queue.Registry) error {
	//nolint:wrapcheck // names the kind
	return reg.Register(DeleteKind, r.remove, queue.HandlerOptions{})
}

const removed = `DELETE FROM blob_objects WHERE store = $1 AND key = $2 AND state = 'deleting'`

// remove deletes a deleting row's object, then the row; a retry after a
// failure between the two finds the object already gone, which every
// driver answers with success.
func (r *Reaper) remove(ctx context.Context, j queue.Job) error {
	var t target
	if err := json.Unmarshal(j.Payload, &t); err != nil {
		//nolint:wrapcheck // Permanent marks the wrapped error
		return queue.Permanent(fmt.Errorf("blobs: %s payload: %w", DeleteKind.Name, err))
	}
	if err := r.s.Delete(ctx, t.Store, t.Key); err != nil {
		if errors.Is(err, objectstore.ErrStore) || errors.Is(err, objectstore.ErrKey) {
			//nolint:wrapcheck // as above; the router's error names the key
			return queue.Permanent(err)
		}
		return err //nolint:wrapcheck // the driver's errors name the key
	}
	if _, err := r.db.Exec(ctx, removed, t.Store, t.Key); err != nil {
		return fmt.Errorf("blobs: remove %s/%s: %w", t.Store, t.Key, sql.Unavailable(err))
	}
	return nil
}

// expire marks up to $2 rows deleting: live rows past expires_at, and
// pending rows older than $1, whose uploads failed or rolled back.
const expire = `
WITH due AS (
    SELECT store, key FROM blob_objects
    WHERE (state = 'live' AND expires_at <= now())
       OR (state = 'pending' AND created_at <= now() - $1::interval)
    LIMIT $2
    FOR UPDATE SKIP LOCKED)
UPDATE blob_objects b SET state = 'deleting'
FROM due WHERE b.store = due.store AND b.key = due.key
RETURNING b.store, b.key`

// Expire is blob.expire: it marks up to limit due objects deleting and
// enqueues blob.delete for each in the same transaction, and returns how
// many it marked.
func (r *Reaper) Expire(ctx context.Context, limit int) (int, error) {
	n := 0
	err := tx.Do(ctx, r.db, tx.Options{}, func(ctx context.Context) error {
		n = 0
		rows, err := tx.Q(ctx, r.db).Query(ctx, expire, Abandoned, limit)
		if err != nil {
			return sql.Unavailable(err) //nolint:wrapcheck // wrapped below
		}
		var due []target
		for rows.Next() {
			var t target
			if err := rows.Scan(&t.Store, &t.Key); err != nil {
				rows.Close()
				return err //nolint:wrapcheck // wrapped below
			}
			due = append(due, t)
		}
		if err := rows.Err(); err != nil {
			return sql.Unavailable(err) //nolint:wrapcheck // wrapped below
		}
		for _, t := range due {
			if _, err := r.q.EnqueueTx(
				ctx,
				DeleteKind,
				t,
				queue.UniqueKey(string(t.Store)+"/"+t.Key),
			); err != nil {
				return err //nolint:wrapcheck // wrapped below
			}
		}
		n = len(due)
		return nil
	})
	if err != nil {
		// A transaction that could not begin is classified here.
		return 0, fmt.Errorf("blobs: expire: %w", sql.Unavailable(err))
	}
	return n, nil
}
