package deps

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type releaseProbe struct {
	tool     Tool
	archive  atomic.Int32
	metadata atomic.Int32
}

type releaseWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *releaseWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

// This real HTTP service isolates transport/caching behavior. The unchanged
// worker scenarios separately verify actual vendor archives and signatures.
func releaseProbeServer(t *testing.T, serve func(http.ResponseWriter, *http.Request, int32, []byte)) *releaseProbe {
	t.Helper()
	p := &releaseProbe{}
	body := []byte("verified pinned release")
	r := chi.NewRouter()
	r.Get("/1.0.0/{file}", func(w http.ResponseWriter, r *http.Request) {
		if chi.URLParam(r, "file") == "tool_linux_amd64" {
			serve(w, r, p.archive.Add(1), body)
			return
		}
		p.metadata.Add(1)
		if chi.URLParam(r, "file") == "SHA256SUMS" {
			_, _ = w.Write([]byte(hexSHA256(body) + "  tool_linux_amd64\n"))
			return
		}
		_, _ = w.Write([]byte("detached signature fixture"))
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	p.tool = Tool{
		Name: "tool", Version: "1.0.0", Download: srv.URL + "/%s/", Bare: true,
		Build: "tool_linux_{arch}", SumsFile: "SHA256SUMS", Signature: ".sig",
		SHA256: map[string]string{"tool_linux_amd64": hexSHA256(body)},
	}
	return p
}

func TestUnitReleaseRetriesTransientHTTPAndWireFailures(t *testing.T) {
	for _, failure := range []string{"service unavailable", "connection closed", "partial body"} {
		t.Run(failure, func(t *testing.T) {
			p := releaseProbeServer(t, func(w http.ResponseWriter, r *http.Request, attempt int32, body []byte) {
				if attempt == 1 {
					switch failure {
					case "service unavailable":
						w.WriteHeader(http.StatusServiceUnavailable)
					case "connection closed":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
					case "partial body":
						w.Header().Set("Content-Length", strconv.Itoa(len(body)+1))
						_, _ = w.Write(body[:3])
					}
					return
				}
				_, _ = w.Write(body)
			})
			d := &Dependencies{ToolCache: t.TempDir()}
			files, err := d.Release(t.Context(), p.tool, "amd64")
			if err != nil || len(files) != 3 || p.archive.Load() != 2 || p.metadata.Load() != 2 {
				t.Fatalf("transient release did not recover once: files=%d archives=%d metadata=%d error=%v", len(files), p.archive.Load(), p.metadata.Load(), err)
			}
			entries, err := os.ReadDir(d.ToolCache)
			if err != nil || len(entries) != 3 {
				t.Fatalf("partial download files escaped: %v %v", entries, err)
			}
			if sum, err := fileSHA256(files[p.tool.Archive("amd64")]); err != nil || sum != p.tool.SHA256[p.tool.Archive("amd64")] {
				t.Fatalf("recovered release bypassed its checksum: %s %v", sum, err)
			}
		})
	}
}

func TestUnitReleaseCoalescesWaitersWithoutSharingCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	p := releaseProbeServer(t, func(w http.ResponseWriter, r *http.Request, attempt int32, body []byte) {
		if attempt == 1 {
			close(entered)
		}
		select {
		case <-release:
			_, _ = w.Write(body)
		case <-r.Context().Done():
		}
	})
	d := &Dependencies{ToolCache: t.TempDir()}
	owner := make(chan error, 1)
	go func() { _, err := d.Release(t.Context(), p.tool, "amd64"); owner <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := d.Release(ctx, p.tool, "amd64"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled release waiter continued: %v", err)
	}
	if p.archive.Load() != 1 {
		t.Fatalf("same cold release fetched concurrently: %d", p.archive.Load())
	}
	finish()
	if err := <-owner; err != nil {
		t.Fatalf("waiter cancelled the owner: %v", err)
	}
	if _, err := d.Release(t.Context(), p.tool, "amd64"); err != nil || p.archive.Load() != 1 || p.metadata.Load() != 2 {
		t.Fatalf("verified release was not shared: archives=%d metadata=%d error=%v", p.archive.Load(), p.metadata.Load(), err)
	}
}

func TestUnitReleaseBoundsRetriesAndKeepsIntegrityFailuresTerminal(t *testing.T) {
	for _, failure := range []string{"forbidden", "unavailable", "wrong checksum", "wrong archive"} {
		t.Run(failure, func(t *testing.T) {
			p := releaseProbeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int32, body []byte) {
				switch failure {
				case "forbidden":
					w.WriteHeader(http.StatusForbidden)
				case "unavailable":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "wrong checksum":
					_, _ = w.Write([]byte("altered release"))
				default:
					_, _ = w.Write(body)
				}
			})
			d := &Dependencies{ToolCache: t.TempDir()}
			t.Cleanup(func() { _ = d.Close(context.Background()) })
			want, attempts := ErrToolDownload, int32(1)
			var err error
			switch failure {
			case "wrong checksum":
				want = ErrToolChecksum
			case "unavailable":
				attempts = 3
			case "wrong archive":
				want = ErrToolArchive
				p.tool.Bare = false
			}
			if failure == "wrong archive" {
				_, err = d.Tool(t.Context(), p.tool, "amd64")
			} else {
				_, err = d.Release(t.Context(), p.tool, "amd64")
			}
			if !errors.Is(err, want) || p.archive.Load() != attempts {
				t.Fatalf("failure retry contract: archives=%d want=%d error=%v want=%v", p.archive.Load(), attempts, err, want)
			}
			if matches, err := filepath.Glob(filepath.Join(d.ToolCache, p.tool.cacheName(p.tool.Archive("amd64"))+".*")); err != nil || len(matches) != 0 {
				t.Fatalf("failed download retained temporary files: %v %v", matches, err)
			}
		})
	}
}

func TestUnitReleaseDoesNotCacheFailedPreparation(t *testing.T) {
	for _, failure := range []string{"permanent response", "retry exhausted", "cancelled backoff", "cancelled owner"} {
		t.Run(failure, func(t *testing.T) {
			var healthy atomic.Bool
			entered := make(chan struct{}, 1)
			p := releaseProbeServer(t, func(w http.ResponseWriter, r *http.Request, _ int32, body []byte) {
				if healthy.Load() {
					_, _ = w.Write(body)
					return
				}
				switch failure {
				case "permanent response":
					w.WriteHeader(http.StatusForbidden)
				case "cancelled owner":
					entered <- struct{}{}
					<-r.Context().Done()
				default:
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			})
			d := &Dependencies{ToolCache: t.TempDir()}
			ctx := t.Context()
			want, attempts := ErrToolDownload, int32(1)
			if failure == "retry exhausted" {
				attempts = 3
			}
			if failure == "cancelled backoff" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
				want = context.DeadlineExceeded
			}
			var err error
			if failure == "cancelled owner" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				result := make(chan error, 1)
				go func() { _, err := d.Release(ctx, p.tool, "amd64"); result <- err }()
				<-entered
				cancel()
				err = <-result
				want = context.Canceled
			} else {
				_, err = d.Release(ctx, p.tool, "amd64")
			}
			if !errors.Is(err, want) || p.archive.Load() != attempts {
				t.Fatalf("failed preparation: archives=%d want=%d error=%v want=%v", p.archive.Load(), attempts, err, want)
			}
			healthy.Store(true)
			files, err := d.Release(t.Context(), p.tool, "amd64")
			if err != nil || len(files) != 3 || p.archive.Load() != attempts+1 || p.metadata.Load() != 2 {
				t.Fatalf("later preparation retained an earlier failure: files=%d archives=%d metadata=%d error=%v", len(files), p.archive.Load(), p.metadata.Load(), err)
			}
			if matches, err := filepath.Glob(filepath.Join(d.ToolCache, p.tool.cacheName(p.tool.Archive("amd64"))+".*")); err != nil || len(matches) != 0 {
				t.Fatalf("cancelled preparation retained temporary files: %v %v", matches, err)
			}
		})
	}
}

func TestUnitReleaseCancelledOwnerDoesNotCancelActiveWaiter(t *testing.T) {
	entered, stopped := make(chan struct{}), make(chan struct{})
	p := releaseProbeServer(t, func(w http.ResponseWriter, r *http.Request, attempt int32, body []byte) {
		if attempt == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body[:3])
			w.(http.Flusher).Flush()
			close(entered)
			<-r.Context().Done()
			close(stopped)
			return
		}
		_, _ = w.Write(body)
	})
	d := &Dependencies{ToolCache: t.TempDir()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner := make(chan error, 1)
	go func() { _, err := d.Release(ctx, p.tool, "amd64"); owner <- err }()
	<-entered
	waitCtx := &releaseWaitContext{Context: t.Context(), entered: make(chan struct{})}
	waiter := make(chan error, 1)
	go func() { _, err := d.Release(waitCtx, p.tool, "amd64"); waiter <- err }()
	// Done is evaluated by the gate's select while the original owner holds it.
	<-waitCtx.entered
	cancel()
	if err := <-owner; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled owner continued: %v", err)
	}
	<-stopped
	if err := <-waiter; err != nil {
		t.Fatalf("owner cancellation escaped into the independent waiter: %v", err)
	}
	if p.archive.Load() != 2 || p.metadata.Load() != 2 {
		t.Fatalf("waiter did not prepare one fresh release: archives=%d metadata=%d", p.archive.Load(), p.metadata.Load())
	}
	entries, err := os.ReadDir(d.ToolCache)
	if err != nil || len(entries) != 3 {
		t.Fatalf("cancelled owner retained partial files: %v %v", entries, err)
	}
}
