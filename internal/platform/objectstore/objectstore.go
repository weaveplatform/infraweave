package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Name is a logical store: a key prefix in a bucket (decision 0005).
type Name string

// The logical stores of decision 0005.
const (
	RunLogs          Name = "run-logs"
	RunArtifacts     Name = "run-artifacts"
	Workspaces       Name = "workspaces"
	States           Name = "states"
	Modules          Name = "modules"
	PolicyInputs     Name = "policy-inputs"
	Uploads          Name = "uploads"
	Deliveries       Name = "deliveries"
	Inbox            Name = "inbox"
	RunObservability Name = "run-observability"
	FlowsEvents      Name = "flows-events"
)

// Names lists every logical store.
func Names() []Name {
	return []Name{
		RunLogs, RunArtifacts, Workspaces, States, Modules, PolicyInputs,
		Uploads, Deliveries, Inbox, RunObservability, FlowsEvents,
	}
}

// Sealed reports whether decision 0013 seals every object of the store
// whatever the bucket's own encryption does.
func (n Name) Sealed() bool { return n == States || n == PolicyInputs || n == RunArtifacts }

// Object is what a write stored: its size and the SHA-256 of its bytes,
// which blob_objects records.
type Object struct {
	Size   int64
	SHA256 [32]byte
}

// Constraints are what a signed PUT URL binds beyond its method, key and
// expiry: the declared size, and the SHA-256 on the drivers that can bind
// it. A GET URL takes none.
type Constraints struct {
	Size   int64
	SHA256 []byte
}

// Signed is a signed request: the URL, and the headers the client must
// send with it because the signature covers them.
type Signed struct {
	URL    string
	Header http.Header
}

var (
	// ErrNotFound reports a key with no object.
	ErrNotFound = errors.New("object not found")
	// ErrSize reports a write whose bytes do not match its declared size.
	ErrSize = errors.New("object size does not match the declared size")
	// ErrKey reports a key that is empty, absolute or climbs out of its
	// store.
	ErrKey = errors.New("invalid object key")
	// ErrMethod reports a signed URL for a method other than GET or PUT.
	ErrMethod = errors.New("signed URLs are GET or PUT")
	// ErrStore reports a name that is not one of the logical stores.
	ErrStore = errors.New("unknown store")
)

// Bucket is what a driver implements, over one bucket and object keys
// within it. A failed Put leaves no readable object, and nothing the
// driver started survives it.
type Bucket interface {
	// Put stores size bytes from r under key and returns their SHA-256.
	// The service verifies what it receives against a checksum the
	// driver sends, part by part when the object is written in parts.
	Put(ctx context.Context, key string, r io.Reader, size int64) (Object, error)
	// Get returns the object's bytes, or ErrNotFound.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Head returns the object's size, or ErrNotFound.
	Head(ctx context.Context, key string) (int64, error)
	// Delete removes the object; a missing key is no error.
	Delete(ctx context.Context, key string) error
	// SignURL returns a request that allows method on key until ttl
	// passes.
	SignURL(
		ctx context.Context,
		method, key string,
		ttl time.Duration,
		c Constraints,
	) (Signed, error)
}

// Writer is the store without Delete, as serve holds it: the identity
// serve runs as needs no delete permission on any bucket.
type Writer interface {
	Put(ctx context.Context, store Name, key string, r io.Reader, size int64) (Object, error)
	Get(ctx context.Context, store Name, key string) (io.ReadCloser, error)
	Head(ctx context.Context, store Name, key string) (int64, error)
	// SignURL takes Config's signed_url_ttl when ttl is 0.
	SignURL(
		ctx context.Context,
		method string,
		store Name,
		key string,
		ttl time.Duration,
		c Constraints,
	) (Signed, error)
}

// Store is the whole store, which only the scheduler holds, for
// blob.delete and blob.expire.
type Store interface {
	Writer
	Delete(ctx context.Context, store Name, key string) error
}

// router is a Store over each logical store's bucket. An object of store
// s with key k is <s>/<k> in its bucket, also when s has a bucket of its
// own, so moving a store copies objects without renaming them.
type router struct {
	buckets map[Name]Bucket
	ttl     time.Duration
}

// New returns the Store over c's buckets, opening each distinct bucket
// once through open: "" is the default bucket, any other name the bucket
// objectstore.buckets gives a store.
func New(c Config, open func(bucket string) (Bucket, error)) (Store, error) {
	opened := map[string]Bucket{}
	r := &router{buckets: map[Name]Bucket{}, ttl: c.SignedURLTTL}
	for _, n := range Names() {
		name := c.Buckets.of(n)
		b, ok := opened[name]
		if !ok {
			var err error
			if b, err = open(name); err != nil {
				return nil, err
			}
			opened[name] = b
		}
		r.buckets[n] = b
	}
	return r, nil
}

// object resolves a store and a key to the bucket and the object key.
func (r *router) object(store Name, key string) (Bucket, string, error) {
	b, ok := r.buckets[store]
	if !ok {
		return nil, "", fmt.Errorf("%w: %q", ErrStore, store)
	}
	if err := CheckKey(key); err != nil {
		return nil, "", err
	}
	return b, string(store) + "/" + key, nil
}

// CheckKey refuses a key that is empty, starts with a slash, holds an
// empty, . or .. segment, or holds a NUL or a backslash: a key names one
// object within its store and never another store's.
func CheckKey(key string) error {
	if key == "" || strings.ContainsAny(key, "\x00\\") {
		return fmt.Errorf("%w: %q", ErrKey, key)
	}
	for seg := range strings.SplitSeq(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: %q", ErrKey, key)
		}
	}
	return nil
}

func (r *router) Put(
	ctx context.Context,
	store Name,
	key string,
	rd io.Reader,
	size int64,
) (Object, error) {
	b, k, err := r.object(store, key)
	if err != nil {
		return Object{}, err
	}
	return b.Put(ctx, k, rd, size) //nolint:wrapcheck // the drivers' errors are the API
}

func (r *router) Get(ctx context.Context, store Name, key string) (io.ReadCloser, error) {
	b, k, err := r.object(store, key)
	if err != nil {
		return nil, err
	}
	return b.Get(ctx, k) //nolint:wrapcheck // as above
}

func (r *router) Head(ctx context.Context, store Name, key string) (int64, error) {
	b, k, err := r.object(store, key)
	if err != nil {
		return 0, err
	}
	return b.Head(ctx, k) //nolint:wrapcheck // as above
}

func (r *router) Delete(ctx context.Context, store Name, key string) error {
	b, k, err := r.object(store, key)
	if err != nil {
		return err
	}
	return b.Delete(ctx, k) //nolint:wrapcheck // as above
}

func (r *router) SignURL(
	ctx context.Context,
	method string,
	store Name,
	key string,
	ttl time.Duration,
	c Constraints,
) (Signed, error) {
	if method != http.MethodGet && method != http.MethodPut {
		return Signed{}, fmt.Errorf("%w: %s", ErrMethod, method)
	}
	b, k, err := r.object(store, key)
	if err != nil {
		return Signed{}, err
	}
	if ttl <= 0 {
		ttl = r.ttl
	}
	return b.SignURL(ctx, method, k, ttl, c) //nolint:wrapcheck // as above
}

// WriterOnly returns s as a Writer that cannot be asserted back to a
// Store, so a process holding it has no path to Delete.
func WriterOnly(s Store) Writer { return writerOnly{s} }

type writerOnly struct{ s Store }

func (w writerOnly) Put(
	ctx context.Context,
	store Name,
	key string,
	r io.Reader,
	size int64,
) (Object, error) {
	return w.s.Put(ctx, store, key, r, size) //nolint:wrapcheck // as above
}

func (w writerOnly) Get(ctx context.Context, store Name, key string) (io.ReadCloser, error) {
	return w.s.Get(ctx, store, key) //nolint:wrapcheck // as above
}

func (w writerOnly) Head(ctx context.Context, store Name, key string) (int64, error) {
	return w.s.Head(ctx, store, key) //nolint:wrapcheck // as above
}

func (w writerOnly) SignURL(
	ctx context.Context,
	method string,
	store Name,
	key string,
	ttl time.Duration,
	c Constraints,
) (Signed, error) {
	return w.s.SignURL(ctx, method, store, key, ttl, c) //nolint:wrapcheck // as above
}
