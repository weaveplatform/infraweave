package gcs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// Bucket is a Cloud Storage bucket.
type Bucket struct {
	bucket   *storage.BucketHandle
	partSize int64
	sign     storage.SignedURLOptions
	now      func() time.Time
}

// Options adjust how the bucket connects and signs, as an emulator's
// tests do. Zero Options are production's: Application Default
// Credentials, and URLs signed through IAM signBlob when they hold no
// private key.
type Options struct {
	Client []option.ClientOption
	// Sign holds a signing identity, GoogleAccessID with a PrivateKey,
	// and Insecure for an emulator that serves plain HTTP.
	Sign storage.SignedURLOptions
}

// New returns bucket, or c.Bucket when bucket is "". Writes go up in
// resumable chunks of partSize.
func New(
	ctx context.Context,
	c objectstore.GCSConfig,
	bucket string,
	partSize int64,
	o Options,
) (*Bucket, error) {
	if bucket == "" {
		bucket = c.Bucket
	}
	client, err := storage.NewClient(ctx, o.Client...)
	if err != nil {
		return nil, fmt.Errorf("gcs: %w", err)
	}
	return &Bucket{
		bucket:   client.Bucket(bucket),
		partSize: partSize,
		sign:     o.Sign,
		now:      time.Now,
	}, nil
}

// Handle is the bucket's SDK handle, for a test that sets the bucket up.
func (b *Bucket) Handle() *storage.BucketHandle { return b.bucket }

// Put streams r to a resumable upload in chunks of the part size,
// hashing as it goes. The SDK sends each chunk's CRC32C, which the
// service checks; Cloud Storage has no SHA-256 it verifies. Only Close
// makes the object, so a source that gives other than size bytes cancels
// the upload and leaves nothing.
func (b *Bucket) Put(
	ctx context.Context,
	key string,
	r io.Reader,
	size int64,
) (objectstore.Object, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := b.bucket.Object(key).NewWriter(ctx)
	w.ChunkSize = int(b.partSize)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(r, size+1))
	if err == nil && n != size {
		err = fmt.Errorf("%w: got %d bytes or more, want %d", objectstore.ErrSize, n, size)
	}
	if err != nil {
		cancel()
		_ = w.Close()
		return objectstore.Object{}, fmt.Errorf("gcs: put %s: %w", key, err)
	}
	if err := w.Close(); err != nil {
		return objectstore.Object{}, fmt.Errorf("gcs: put %s: %w", key, err)
	}
	obj := objectstore.Object{Size: size}
	h.Sum(obj.SHA256[:0])
	return obj, nil
}

func notFound(op, key string, err error) error {
	if errors.Is(err, storage.ErrObjectNotExist) {
		return objectstore.ErrNotFound
	}
	return fmt.Errorf("gcs: %s %s: %w", op, key, err)
}

// Get streams the object.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := b.bucket.Object(key).NewReader(ctx)
	if err != nil {
		return nil, notFound("get", key, err)
	}
	return r, nil
}

// Head returns the object's size.
func (b *Bucket) Head(ctx context.Context, key string) (int64, error) {
	a, err := b.bucket.Object(key).Attrs(ctx)
	if err != nil {
		return 0, notFound("head", key, err)
	}
	return a.Size, nil
}

// Delete removes the object; a missing key is no error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	if err := b.bucket.Object(key).
		Delete(ctx); err != nil &&
		!errors.Is(err, storage.ErrObjectNotExist) {
		return fmt.Errorf("gcs: delete %s: %w", key, err)
	}
	return nil
}

// SignURL signs a V4 URL. A PUT binds Content-Length as a signed header;
// Cloud Storage cannot bind a SHA-256, so Commit reads the object back.
func (b *Bucket) SignURL(
	_ context.Context,
	method, key string,
	ttl time.Duration,
	c objectstore.Constraints,
) (objectstore.Signed, error) {
	if method != http.MethodGet && method != http.MethodPut {
		return objectstore.Signed{}, fmt.Errorf("%w: %s", objectstore.ErrMethod, method)
	}
	o := b.sign
	o.Method, o.Expires, o.Scheme = method, b.now().Add(ttl), storage.SigningSchemeV4
	h := http.Header{}
	if method == http.MethodPut {
		length := strconv.FormatInt(c.Size, 10)
		o.Headers = []string{"Content-Length:" + length}
		h.Set("Content-Length", length)
	}
	u, err := b.bucket.SignedURL(key, &o)
	if err != nil {
		return objectstore.Signed{}, fmt.Errorf("gcs: sign %s %s: %w", method, key, err)
	}
	return objectstore.Signed{URL: u, Header: h}, nil
}
