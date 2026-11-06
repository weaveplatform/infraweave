package objectstoretest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// Options describe the bucket under test.
type Options struct {
	// PartSize is the bucket's part size, which the multipart cases write
	// past.
	PartSize int64
	// Skip names the cases the bucket cannot decide, each with its reason:
	// Gaps and Unbound for the driver.
	Skip map[string]string
	// Client sends the signed requests; nil is http.DefaultClient.
	Client *http.Client
	// Identity is what the bucket's credentials may do.
	Identity Identity
}

// Identity is what the credentials under test may do. The
// objectstore-cloud job runs the contract as each of decision 0020's two
// identities in turn; an emulator's credentials may do everything.
type Identity int

const (
	// Full credentials run every case except delete-refused.
	Full Identity = iota
	// Writer is serve's identity, which reads and writes and may not
	// delete: delete-refused runs in place of delete.
	Writer
	// Deleter is the scheduler's identity, which may also delete.
	Deleter
)

// Case is one clause of the contract.
type Case struct {
	Name string
	run  func(t *testing.T, e env)
}

type env struct {
	b       objectstore.Bucket
	o       Options
	prefix  string
	written *written
}

// written records every key a run names, so a run whose credentials may
// delete removes what it wrote; a writer's run leaves its keys to the
// bucket's lifecycle rule.
type written struct {
	mu   sync.Mutex
	keys []string
}

func (w *written) add(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.keys = append(w.keys, key)
}

// Cases is the contract, in the order Run runs it.
func Cases() []Case {
	return []Case{
		{"round-trip", roundTrip},
		{"empty", empty},
		{"missing", missing},
		{"delete", deleteCase},
		{"delete-refused", deleteRefused},
		{"short-write", shortWrite},
		{"long-write", longWrite},
		{"multipart", multipart},
		{"multipart-failure", multipartFailure},
		{"signed-get", signedGet},
		{"signed-put", signedPut},
		{"signed-put-size", signedPutSize},
		{"signed-put-sha256", signedPutSHA256},
		{"signed-method", signedMethod},
		{"signed-tampered", signedTampered},
		{"signed-expired", signedExpired},
	}
}

// Gaps are the cases an emulator cannot decide, by driver. Each runs
// against the real service in the objectstore-cloud job, where Skip is
// Unbound alone.
var Gaps = map[string]map[string]string{
	objectstore.DriverGCS: {
		"signed-put-size": "fake-gcs-server does not verify signed URLs",
		"signed-method":   "fake-gcs-server does not verify signed URLs",
		"signed-tampered": "fake-gcs-server does not verify signed URLs",
		"signed-expired":  "fake-gcs-server does not verify signed URLs",
	},
}

// Unbound are the cases a service itself cannot meet, by driver; the
// contract covers them elsewhere, as the reason says.
var Unbound = map[string]map[string]string{
	objectstore.DriverGCS: {
		"signed-put-sha256": "Cloud Storage cannot bind a SHA-256; Commit reads the object back and compares",
	},
	objectstore.DriverAzureBlob: {
		"signed-put-size":   "a SAS cannot bind Content-Length; Commit's Head refuses another size",
		"signed-put-sha256": "a SAS cannot bind a SHA-256; Commit reads the object back and compares",
	},
}

// Skip is what a driver skips against its emulator: its gaps and what it
// cannot bind.
func Skip(driver string) map[string]string {
	out := map[string]string{}
	for _, m := range []map[string]string{Gaps[driver], Unbound[driver]} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// Run runs every case of the contract against b, each under its own key
// prefix, skipping o.Skip's by name with their reasons.
func Run(t *testing.T, b objectstore.Bucket, o Options) {
	t.Helper()
	if o.Client == nil {
		o.Client = http.DefaultClient
	}
	var run [8]byte
	_, _ = rand.Read(run[:])
	w := &written{}
	if o.Identity != Writer {
		// The cases' contexts have ended by now.
		t.Cleanup(func() {
			for _, k := range w.keys {
				if err := b.Delete(context.Background(), k); err != nil {
					t.Errorf("removing %s: %v", k, err)
				}
			}
		})
	}
	for _, c := range Cases() {
		t.Run(c.Name, func(t *testing.T) {
			if why, ok := o.Skip[c.Name]; ok {
				t.Skip(why)
			}
			switch {
			case c.Name == "delete" && o.Identity == Writer:
				t.Skip("the writer may not delete; delete-refused runs instead")
			case c.Name == "delete-refused" && o.Identity != Writer:
				t.Skip("only the writer is refused a delete")
			}
			c.run(t, env{
				b:       b,
				o:       o,
				prefix:  "contract/" + hex.EncodeToString(run[:]) + "/" + c.Name,
				written: w,
			})
		})
	}
}

func (e env) key(leaf string) string {
	k := e.prefix + "/" + leaf
	e.written.add(k)
	return k
}

func random(n int64) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func (e env) put(t *testing.T, key string, body []byte) {
	t.Helper()
	obj, err := e.b.Put(t.Context(), key, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("Put %s: %v", key, err)
	}
	if obj.Size != int64(len(body)) || obj.SHA256 != sha256.Sum256(body) {
		t.Fatalf("Put %s returned %d bytes, SHA-256 %x", key, obj.Size, obj.SHA256)
	}
}

func (e env) expect(t *testing.T, key string, body []byte) {
	t.Helper()
	if size, err := e.b.Head(t.Context(), key); err != nil || size != int64(len(body)) {
		t.Fatalf("Head %s: %d, %v; want %d", key, size, err, len(body))
	}
	r, err := e.b.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("Get %s: %d bytes back, %v; want %d", key, len(got), err, len(body))
	}
}

func (e env) absent(t *testing.T, key string) {
	t.Helper()
	if _, err := e.b.Head(t.Context(), key); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("Head %s: %v, want ErrNotFound", key, err)
	}
	if r, err := e.b.Get(t.Context(), key); !errors.Is(err, objectstore.ErrNotFound) {
		if err == nil {
			r.Close()
		}
		t.Fatalf("Get %s: %v, want ErrNotFound", key, err)
	}
}

func roundTrip(t *testing.T, e env) {
	body := random(1000)
	e.put(t, e.key("a"), body)
	e.expect(t, e.key("a"), body)
}

func empty(t *testing.T, e env) {
	e.put(t, e.key("a"), nil)
	e.expect(t, e.key("a"), nil)
}

func missing(t *testing.T, e env) {
	e.absent(t, e.key("never-written"))
}

func deleteCase(t *testing.T, e env) {
	e.put(t, e.key("a"), random(10))
	if err := e.b.Delete(t.Context(), e.key("a")); err != nil {
		t.Fatal(err)
	}
	e.absent(t, e.key("a"))
	if err := e.b.Delete(t.Context(), e.key("never-written")); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
}

// deleteRefused checks that serve's identity cannot delete, so serve
// needs no delete permission (decision 0005): the delete fails and the
// object stays.
func deleteRefused(t *testing.T, e env) {
	body := random(10)
	e.put(t, e.key("a"), body)
	if err := e.b.Delete(t.Context(), e.key("a")); err == nil {
		t.Fatal("the writer deleted an object")
	}
	e.expect(t, e.key("a"), body)
}

func shortWrite(t *testing.T, e env) {
	if _, err := e.b.Put(t.Context(), e.key("a"), bytes.NewReader(random(5)), 10); err == nil {
		t.Fatal("a write of 5 bytes declared as 10 succeeded")
	}
	e.absent(t, e.key("a"))
}

func longWrite(t *testing.T, e env) {
	if _, err := e.b.Put(t.Context(), e.key("a"), bytes.NewReader(random(10)), 5); err == nil {
		t.Fatal("a write of 10 bytes declared as 5 succeeded")
	}
	e.absent(t, e.key("a"))
}

func multipart(t *testing.T, e env) {
	body := random(2*e.o.PartSize + 1)
	e.put(t, e.key("a"), body)
	e.expect(t, e.key("a"), body)
}

var errBroken = errors.New("the source failed")

// failingReader returns n bytes of r, then errBroken.
type failingReader struct {
	r io.Reader
	n int64
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errBroken
	}
	p = p[:min(int64(len(p)), f.n)]
	n, err := f.r.Read(p)
	f.n -= int64(n)
	return n, err //nolint:wrapcheck // the test's own reader
}

func multipartFailure(t *testing.T, e env) {
	size := 2*e.o.PartSize + 1
	r := &failingReader{r: bytes.NewReader(random(size)), n: e.o.PartSize + 1}
	if _, err := e.b.Put(t.Context(), e.key("a"), r, size); err == nil {
		t.Fatal("a write whose source failed after a part succeeded")
	}
	e.absent(t, e.key("a"))
}

// send makes the signed request with body, or none when body is nil,
// and returns the status and the response body.
func (e env) send(t *testing.T, method string, s objectstore.Signed, body []byte) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, s.URL, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range s.Header {
		req.Header[k] = v
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	resp, err := e.o.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

func ok(status int) bool { return status >= 200 && status < 300 }

func (e env) sign(
	t *testing.T,
	method, key string,
	ttl time.Duration,
	c objectstore.Constraints,
) objectstore.Signed {
	t.Helper()
	s, err := e.b.SignURL(t.Context(), method, key, ttl, c)
	if err != nil {
		t.Fatalf("SignURL %s %s: %v", method, key, err)
	}
	return s
}

func putConstraints(body []byte) objectstore.Constraints {
	sum := sha256.Sum256(body)
	return objectstore.Constraints{Size: int64(len(body)), SHA256: sum[:]}
}

func signedGet(t *testing.T, e env) {
	body := random(300)
	e.put(t, e.key("a"), body)
	status, got := e.send(
		t,
		http.MethodGet,
		e.sign(t, http.MethodGet, e.key("a"), time.Minute, objectstore.Constraints{}),
		nil,
	)
	if !ok(status) || !bytes.Equal(got, body) {
		t.Fatalf("GET %d with %d bytes", status, len(got))
	}
}

func signedPut(t *testing.T, e env) {
	body := random(300)
	status, resp := e.send(
		t,
		http.MethodPut,
		e.sign(t, http.MethodPut, e.key("a"), time.Minute, putConstraints(body)),
		body,
	)
	if !ok(status) {
		t.Fatalf("PUT %d: %s", status, resp)
	}
	e.expect(t, e.key("a"), body)
}

func signedPutSize(t *testing.T, e env) {
	declared := random(300)
	s := e.sign(t, http.MethodPut, e.key("a"), time.Minute, objectstore.Constraints{Size: 300})
	if status, _ := e.send(t, http.MethodPut, s, append(declared, 1)); ok(status) {
		t.Fatalf("PUT of 301 bytes to a 300-byte URL answered %d", status)
	}
	e.absent(t, e.key("a"))
}

func signedPutSHA256(t *testing.T, e env) {
	declared, sent := random(300), random(300)
	s := e.sign(t, http.MethodPut, e.key("a"), time.Minute, putConstraints(declared))
	if status, _ := e.send(t, http.MethodPut, s, sent); ok(status) {
		t.Fatalf("PUT of other bytes answered %d", status)
	}
	e.absent(t, e.key("a"))
}

func signedMethod(t *testing.T, e env) {
	body := random(300)
	e.put(t, e.key("a"), body)
	get := e.sign(t, http.MethodGet, e.key("a"), time.Minute, objectstore.Constraints{})
	if status, _ := e.send(t, http.MethodPut, get, random(300)); ok(status) {
		t.Errorf("PUT through a GET URL answered %d", status)
	}
	e.expect(t, e.key("a"), body)
	put := e.sign(t, http.MethodPut, e.key("b"), time.Minute, putConstraints(body))
	if status, _ := e.send(t, http.MethodGet, put, nil); ok(status) {
		t.Errorf("GET through a PUT URL answered %d", status)
	}
}

// signatureParams are where each service carries a signed URL's
// signature; a URL with none of them carries it in its last path segment.
var signatureParams = []string{"X-Amz-Signature", "X-Goog-Signature", "sig"}

// tamper changes one character of s's signature.
func tamper(t *testing.T, s objectstore.Signed) objectstore.Signed {
	t.Helper()
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	flip := func(v string) string {
		i := len(v) / 2
		c := byte('A')
		if v[i] == 'A' {
			c = 'B'
		}
		return v[:i] + string(c) + v[i+1:]
	}
	q := u.Query()
	for _, p := range signatureParams {
		if v := q.Get(p); v != "" {
			q.Set(p, flip(v))
			u.RawQuery = q.Encode()
			return objectstore.Signed{URL: u.String(), Header: s.Header}
		}
	}
	i := strings.LastIndex(u.Path, "/")
	u.Path = u.Path[:i+1] + flip(u.Path[i+1:])
	return objectstore.Signed{URL: u.String(), Header: s.Header}
}

func signedTampered(t *testing.T, e env) {
	e.put(t, e.key("a"), random(300))
	s := tamper(t, e.sign(t, http.MethodGet, e.key("a"), time.Minute, objectstore.Constraints{}))
	if status, _ := e.send(t, http.MethodGet, s, nil); ok(status) {
		t.Errorf("GET through an altered URL answered %d", status)
	}
}

// signedExpired signs for one second and asks until the URL is refused,
// within a bound: no wait is fixed, so a slow clock costs time, not a
// failure.
func signedExpired(t *testing.T, e env) {
	e.put(t, e.key("a"), random(300))
	s := e.sign(t, http.MethodGet, e.key("a"), time.Second, objectstore.Constraints{})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var seen []int
	for {
		status, _ := e.send(t, http.MethodGet, s, nil)
		if !ok(status) {
			return
		}
		seen = append(seen, status)
		select {
		case <-ctx.Done():
			t.Fatalf("a one-second URL still answers after 30s: %v", slices.Compact(seen))
		case <-tick.C:
		}
	}
}
