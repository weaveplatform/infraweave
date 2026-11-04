package specref

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// upstream models the three services fetch talks to: a GraphQL endpoint
// that answers introspection, and codeload serving the two tarballs. Each
// fails its first failures requests with 503 to exercise retries.
type upstream struct {
	srv       *httptest.Server
	failures  int32
	hits      atomic.Int32
	intro     []byte
	bruno     []byte
	provider  []byte
	userAgent atomic.Value
}

func newUpstream(t *testing.T, failures int32) *upstream {
	t.Helper()
	intro, err := MarshalIntrospection(fixtureSchema())
	if err != nil {
		t.Fatal(err)
	}
	u := &upstream{
		failures: failures,
		intro:    intro,
		bruno: tarball(t, map[string]string{
			"Spacelift/Stacks/folder.bru":       "meta {\n  name: Stacks\n}\n",
			"Spacelift/Stacks/Create Stack.bru": bruRequest,
		}),
		provider: tarball(t, map[string]string{"spacelift/resource_stack.go": providerResource}),
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			u.userAgent.Store(req.UserAgent())
			if u.hits.Add(1) <= u.failures {
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Post("/graphql", func(w http.ResponseWriter, req *http.Request) {
		var body struct{ Query string }
		if json.NewDecoder(req.Body).Decode(&body) != nil ||
			!strings.Contains(body.Query, "__schema") {
			http.Error(w, "not an introspection query", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(u.intro)
	})
	r.Get("/{owner}/{repo}/tar.gz/*", func(w http.ResponseWriter, req *http.Request) {
		switch chi.URLParam(req, "repo") {
		case "spacelift-api-bruno":
			_, _ = w.Write(u.bruno)
		case "terraform-provider-spacelift":
			_, _ = w.Write(u.provider)
		default:
			http.NotFound(w, req)
		}
	})
	u.srv = httptest.NewServer(r)
	t.Cleanup(u.srv.Close)
	return u
}

func testFetcher() *Fetcher {
	return &Fetcher{
		Client:    http.DefaultClient,
		UserAgent: "specref-test",
		Retries:   2,
		Backoff:   time.Millisecond,
	}
}

func TestAccFetcherRetries(t *testing.T) {
	u := newUpstream(t, 2)
	f := testFetcher()
	got, err := f.Introspect(context.Background(), u.srv.URL+"/graphql")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(u.intro) || u.hits.Load() != 3 {
		t.Errorf("hits = %d", u.hits.Load())
	}
	if ua := u.userAgent.Load(); ua != "specref-test" {
		t.Errorf("user agent = %v", ua)
	}

	u = newUpstream(t, 5)
	if _, err := f.Get(
		context.Background(),
		u.srv.URL+"/a/spacelift-api-bruno/tar.gz/x",
	); !errors.Is(
		err,
		ErrHTTPStatus,
	) {
		t.Errorf("retries exhausted: err = %v", err)
	}

	u = newUpstream(t, 0)
	if _, err := f.Get(
		context.Background(),
		u.srv.URL+"/a/unknown/tar.gz/x",
	); !errors.Is(err, ErrHTTPStatus) ||
		u.hits.Load() != 1 {
		t.Errorf("404 is not retried: err = %v hits = %d", err, u.hits.Load())
	}
}

func TestAccFetcherFailures(t *testing.T) {
	f := testFetcher()
	if _, err := f.Get(context.Background(), "http://127.0.0.1:1/unreachable"); err == nil {
		t.Error("unreachable: want error")
	}
	if _, err := f.Get(context.Background(), "::bad url"); err == nil {
		t.Error("bad url: want error")
	}
	u := newUpstream(t, 100)
	ctx, cancel := context.WithCancel(context.Background())
	slow := &Fetcher{Client: http.DefaultClient, Retries: 3, Backoff: time.Hour}
	go func() {
		for u.hits.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if _, err := slow.Get(ctx, u.srv.URL+"/x"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel during backoff: err = %v", err)
	}
	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	}))
	defer truncated.Close()
	if _, err := testFetcher().Get(context.Background(), truncated.URL); err == nil {
		t.Error("truncated body: want error")
	}
}

func fetchOptions(t *testing.T, u *upstream, dir string) FetchOptions {
	t.Helper()
	return FetchOptions{
		Dir:             dir,
		Endpoint:        u.srv.URL + "/graphql",
		Codeload:        u.srv.URL,
		BrunoRepo:       "spacelift-io/spacelift-api-bruno",
		BrunoRef:        "7cad4574954b569df083b2a809b68371fc385e6d",
		ProviderRepo:    "spacelift-io/terraform-provider-spacelift",
		ProviderVersion: "1.57.0",
		Terraform:       "terraform",
		Fetcher:         testFetcher(),
		Run:             func(context.Context, string, string, ...string) ([]byte, error) { return []byte(terraformJSON), nil },
		Now:             func() time.Time { return time.Date(2026, 10, 28, 7, 0, 0, 0, time.UTC) },
	}
}

func TestAccFetch(t *testing.T) {
	u := newUpstream(t, 1)
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, FileMapping),
		[]byte(fixtureMapping),
		filePerm,
	); err != nil {
		t.Fatal(err)
	}
	if err := Fetch(context.Background(), fetchOptions(t, u, dir)); err != nil {
		t.Fatal(err)
	}
	src, err := ReadSources(dir)
	if err != nil {
		t.Fatal(err)
	}
	if src.Lock.Introspection.FetchedAt != "2026-10-28T07:00:00Z" ||
		src.Lock.Provider.Ref != "v1.57.0" ||
		len(src.Lock.Bruno.SHA256) != 64 {
		t.Errorf("lock = %+v", src.Lock)
	}
	if len(src.Bruno.Requests) != 1 ||
		src.TerraformOps.Resources["spacelift_stack"][0] != "stack" ||
		src.Terraform.Resources["spacelift_stack"].Description != "A stack." {
		t.Errorf("sources = %+v %+v", src.Bruno, src.TerraformOps)
	}
	if _, err := GenerateDir(context.Background(), dir); err != nil {
		t.Fatalf("a fresh fetch must generate: %v", err)
	}
}

func TestAccFetchFailures(t *testing.T) {
	u := newUpstream(t, 0)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json and not a tarball")
	}))
	defer broken.Close()
	tests := map[string]func(*FetchOptions){
		"branch ref":  func(o *FetchOptions) { o.BrunoRef = "main" },
		"no endpoint": func(o *FetchOptions) { o.Endpoint = u.srv.URL + "/missing" },
		"bad schema":  func(o *FetchOptions) { o.Endpoint = broken.URL },
		"no bruno":    func(o *FetchOptions) { o.BrunoRepo = "x/unknown" },
		"bad bruno": func(o *FetchOptions) {
			o.BrunoRepo = "x/terraform-provider-spacelift-nope"
			o.Codeload = broken.URL
			o.Endpoint = u.srv.URL + "/graphql"
		},
		"no provider": func(o *FetchOptions) { o.ProviderRepo = "x/unknown" },
		"terraform fails": func(o *FetchOptions) {
			o.Run = func(context.Context, string, string, ...string) ([]byte, error) {
				return nil, errors.New("no terraform")
			}
		},
		"terraform output": func(o *FetchOptions) {
			o.Run = func(context.Context, string, string, ...string) ([]byte, error) { return []byte("{}"), nil }
		},
		"bad dir": func(o *FetchOptions) { o.Dir = filepath.Join(o.Dir, "missing") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			o := fetchOptions(t, u, dir)
			mutate(&o)
			if err := Fetch(context.Background(), o); err == nil {
				t.Fatal("want error")
			}
			if name != "bad dir" {
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Errorf("a failed fetch wrote %d files", len(entries))
				}
			}
		})
	}
	t.Run("bad provider tarball", func(t *testing.T) {
		o := fetchOptions(t, u, t.TempDir())
		bad := chi.NewRouter()
		bad.Get(
			"/{owner}/spacelift-api-bruno/tar.gz/*",
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(u.bruno) },
		)
		bad.Get(
			"/{owner}/terraform-provider-spacelift/tar.gz/*",
			func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "nope") },
		)
		srv := httptest.NewServer(bad)
		defer srv.Close()
		o.Codeload = srv.URL
		if err := Fetch(context.Background(), o); err == nil {
			t.Fatal("want error")
		}
	})
}
