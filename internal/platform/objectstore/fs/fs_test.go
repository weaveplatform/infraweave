package fs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
)

// keys derives from fixed keys 1 and 2, of which 2 is active.
type keys struct{ active uint32 }

func (k keys) DeriveKey(purpose string) ([]byte, uint32, error) {
	d, err := k.DerivedKey(purpose, k.active)
	return d, k.active, err
}

var errUnknown = errors.New("unknown key")

func (keys) DerivedKey(purpose string, id uint32) ([]byte, error) {
	if id != 1 && id != 2 {
		return nil, errUnknown
	}
	return bytes.Repeat([]byte{byte(id)}, 32), nil
}

// serve starts a bucket under a temporary root, with its routes on a
// test server whose address its signed URLs carry.
func serve(t *testing.T, k fs.Keys) (*fs.Bucket, string) {
	t.Helper()
	r := chi.NewRouter()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	root := filepath.Join(t.TempDir(), "objects")
	b, err := fs.New(objectstore.FSConfig{Root: root, URL: srv.URL}, k)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	b.Routes(r)
	return b, root
}

func TestUnitFSContract(t *testing.T) {
	b, _ := serve(t, keys{active: 2})
	objectstoretest.Run(t, b, objectstoretest.Options{
		PartSize: objectstore.MinPartSize,
		Skip:     objectstoretest.Skip(objectstore.DriverFS),
	})
}

// TestUnitFSRotatedKey checks that a URL signed before a rotation still
// verifies while its key is retiring.
func TestUnitFSRotatedKey(t *testing.T) {
	r := chi.NewRouter()
	srv := httptest.NewServer(r)
	defer srv.Close()
	root := t.TempDir()
	old, err := fs.New(objectstore.FSConfig{Root: root, URL: srv.URL}, keys{active: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := old.Put(t.Context(), "s/a", strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	s, err := old.SignURL(
		t.Context(),
		http.MethodGet,
		"s/a",
		time.Minute,
		objectstore.Constraints{},
	)
	if err != nil {
		t.Fatal(err)
	}
	now, err := fs.New(objectstore.FSConfig{Root: root, URL: srv.URL}, keys{active: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer now.Close()
	now.Routes(r)
	resp, err := http.Get(s.URL) //nolint:noctx // a test request
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a URL signed under the retiring key: %d", resp.StatusCode)
	}
}

func status(t *testing.T, method, u string, body io.Reader, length int64) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = length
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestUnitFSTokens checks the answers of /blobs/{token} beyond the
// contract's: which status each refusal gets.
func TestUnitFSTokens(t *testing.T) {
	b, _ := serve(t, keys{active: 2})
	ctx := t.Context()
	sign := func(method, key string, c objectstore.Constraints) string {
		s, err := b.SignURL(ctx, method, key, time.Minute, c)
		if err != nil {
			t.Fatal(err)
		}
		return s.URL
	}
	put := sign(http.MethodPut, "s/a", objectstore.Constraints{Size: 3})
	for name, tc := range map[string]struct {
		method, url string
		body        string
		length      int64
		want        int
	}{
		"larger":         {http.MethodPut, put, "abcd", 4, http.StatusRequestEntityTooLarge},
		"smaller":        {http.MethodPut, put, "ab", 2, http.StatusBadRequest},
		"no length":      {http.MethodPut, put, "abc", -1, http.StatusBadRequest},
		"not base64":     {http.MethodGet, strings.TrimSuffix(put, put[len(put)-60:]) + "!!!", "", 0, http.StatusForbidden},
		"too short":      {http.MethodGet, put[:strings.LastIndex(put, "/")+1] + "AQ", "", 0, http.StatusForbidden},
		"missing object": {http.MethodGet, sign(http.MethodGet, "s/none", objectstore.Constraints{}), "", 0, http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			if got, text := status(t, tc.method, tc.url, body, tc.length); got != tc.want {
				t.Errorf("%d %s, want %d", got, text, tc.want)
			}
		})
	}
	if _, err := b.SignURL(
		ctx,
		http.MethodDelete,
		"s/a",
		time.Minute,
		objectstore.Constraints{},
	); !errors.Is(
		err,
		objectstore.ErrMethod,
	) {
		t.Errorf("DELETE URL: %v", err)
	}
	if _, err := b.SignURL(
		ctx,
		http.MethodPut,
		"s/a",
		time.Minute,
		objectstore.Constraints{Size: 1, SHA256: []byte{1}},
	); !errors.Is(
		err,
		fs.ErrToken,
	) {
		t.Errorf("a 1-byte SHA-256: %v", err)
	}
	unknown, _ := serve(t, keys{active: 3})
	if _, err := unknown.SignURL(
		ctx,
		http.MethodGet,
		"s/a",
		time.Minute,
		objectstore.Constraints{},
	); err == nil {
		t.Error("signed without a key")
	}
}

// TestUnitFSLayout checks where objects live, that a directory is no
// object, and that no write leaves a temporary file behind.
func TestUnitFSLayout(t *testing.T) {
	b, root := serve(t, keys{active: 2})
	ctx := t.Context()
	if _, err := b.Put(ctx, "states/acct/1", strings.NewReader("abc"), 3); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(
		filepath.Join(root, "states", "acct", "1"),
	); err != nil ||
		string(got) != "abc" {
		t.Errorf("on disk: %q, %v", got, err)
	}
	for _, key := range []string{"states/acct", "states/acct/1/under"} {
		if _, err := b.Head(ctx, key); !errors.Is(err, objectstore.ErrNotFound) {
			t.Errorf("Head %s: %v", key, err)
		}
		if _, err := b.Get(ctx, key); !errors.Is(err, objectstore.ErrNotFound) {
			t.Errorf("Get %s: %v", key, err)
		}
	}
	if _, err := b.Put(ctx, "states/acct/2", strings.NewReader("ab"), 3); err == nil {
		t.Error("short write")
	}
	if left, _ := os.ReadDir(filepath.Join(root, ".tmp")); len(left) != 0 {
		t.Errorf("temporary files left: %d", len(left))
	}
	if _, err := b.Put(ctx, "../escape", strings.NewReader("x"), 1); err == nil {
		t.Error("a key outside the root was written")
	}
	if err := b.Delete(ctx, "states/acct"); err != nil {
		t.Errorf("deleting a directory's name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "states", "acct", "1")); err != nil {
		t.Errorf("deleting a directory's name removed its objects: %v", err)
	}
	if _, err := b.Put(ctx, "states/acct/1/under", strings.NewReader("x"), 1); err == nil {
		t.Error("a key under an object was written")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := b.Put(
		cancelled,
		"states/acct/3",
		strings.NewReader("abc"),
		3,
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Errorf("a cancelled write: %v", err)
	}
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, ".tmp"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.New(objectstore.FSConfig{Root: blocked}, keys{}); err == nil {
		t.Error("a root whose .tmp is a file opened")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.New(
		objectstore.FSConfig{Root: filepath.Join(file, "sub")},
		keys{},
	); err == nil {
		t.Error("a root under a file opened")
	}
}

// TestUnitFSMovingTransfers checks, on a listener whose read and write
// timeouts are far shorter than the transfers, that an upload trickled
// past them and a download read slowly past them both complete while
// they keep moving, and that an upload that stalls for longer than Stall
// loses its connection and stores nothing.
func TestUnitFSMovingTransfers(t *testing.T) {
	r := chi.NewRouter()
	srv := httptest.NewUnstartedServer(r)
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 300*time.Millisecond, 300*time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	b, err := fs.New(objectstore.FSConfig{
		Root: filepath.Join(t.TempDir(), "objects"), URL: srv.URL,
	}, keys{active: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	fs.SetStall(b, time.Second)
	b.Routes(r)
	ctx := t.Context()
	sign := func(method, key string, size int64) string {
		s, err := b.SignURL(ctx, method, key, time.Minute, objectstore.Constraints{Size: size})
		if err != nil {
			t.Fatal(err)
		}
		return s.URL
	}
	// upload sends size bytes in chunks, each after wait.
	upload := func(key string, size, chunks int, wait time.Duration) (int, error) {
		pr, pw := io.Pipe()
		go func() {
			for range chunks {
				<-time.After(wait)
				if _, err := pw.Write(bytes.Repeat([]byte("x"), size/chunks)); err != nil {
					return
				}
			}
			_ = pw.Close()
		}()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, sign(http.MethodPut, key,
			int64(size)), pr)
		req.ContentLength = int64(size)
		resp, err := http.DefaultClient.Do(req)
		_ = pr.Close()
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}
	if code, err := upload("s/trickled", 6<<10, 6, 150*time.Millisecond); err != nil ||
		code != http.StatusOK {
		t.Errorf("a trickled upload = %d, %v", code, err)
	}
	if code, err := upload("s/stalled", 2<<10, 2, 2*time.Second); err == nil &&
		code == http.StatusOK {
		t.Error("a stalled upload completed")
	}
	if _, err := b.Head(ctx, "s/stalled"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("a stalled upload stored: %v", err)
	}

	const size = 16 << 20
	if _, err := b.Put(ctx, "s/large", bytes.NewReader(make([]byte, size)), size); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sign(http.MethodGet, "s/large", 0),
		nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, buf := 0, make([]byte, 64<<10)
	for {
		n, err := io.ReadFull(resp.Body, buf)
		got += n
		if err != nil {
			break
		}
		<-time.After(5 * time.Millisecond)
	}
	if got != size {
		t.Errorf("a slow download read %d of %d bytes", got, size)
	}
}
