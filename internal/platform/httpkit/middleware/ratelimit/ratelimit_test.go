package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// clock is a settable time source, so refill is tested without sleeping.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock                      { return &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }
func ok(http.ResponseWriter, *http.Request) {}

func TestUnitBucketRefillAndBurst(t *testing.T) {
	c := newClock()
	m := NewMemory(c.now)
	l := Limit{Rate: 2, Burst: 3}
	k := Key{Caller: "a", Class: Standard}
	take := func() State {
		t.Helper()
		st, err := m.Take(t.Context(), k, l)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	// A new bucket starts full and admits a whole burst at once.
	for want := 2; want >= 0; want-- {
		if st := take(); !st.Allowed || st.Remaining != want {
			t.Fatalf("burst: %+v, want allowed with %d left", st, want)
		}
	}
	st := take()
	if st.Allowed || st.RetryAfter != 500*time.Millisecond || st.Reset != 1500*time.Millisecond {
		t.Fatalf("empty: %+v", st)
	}
	// Half a second at 2 a second is one token.
	c.advance(500 * time.Millisecond)
	if st := take(); !st.Allowed || st.Remaining != 0 {
		t.Fatalf("after refill: %+v", st)
	}
	// Refill stops at the burst.
	c.advance(time.Hour)
	if st := take(); st.Remaining != 2 || st.Reset != 500*time.Millisecond {
		t.Fatalf("after a long idle: %+v", st)
	}
	// A clock stepping back neither refills nor drains.
	c.advance(-time.Minute)
	if st := take(); !st.Allowed || st.Remaining != 1 {
		t.Fatalf("clock stepped back: %+v", st)
	}
}

func TestUnitBucketsAreSeparate(t *testing.T) {
	c := newClock()
	m := NewMemory(c.now)
	l := Limit{Rate: 1, Burst: 1}
	for _, k := range []Key{{"a", Standard}, {"a", Expensive}, {"b", Standard}} {
		if st, _ := m.Take(t.Context(), k, l); !st.Allowed {
			t.Errorf("%+v: first request refused", k)
		}
	}
	if st, _ := m.Take(t.Context(), Key{"a", Standard}, l); st.Allowed {
		t.Error("a's standard bucket admitted a second request")
	}
}

func TestUnitMemorySweepsFullBuckets(t *testing.T) {
	c := newClock()
	m := NewMemory(c.now)
	l := Limit{Rate: 1, Burst: 10}
	_, _ = m.Take(t.Context(), Key{"idle", Standard}, l)
	c.advance(sweepEvery)
	_, _ = m.Take(t.Context(), Key{"busy", Standard}, l)
	if _, kept := m.buckets[Key{"idle", Standard}]; kept {
		t.Error("a refilled bucket survived the sweep")
	}
	if _, kept := m.buckets[Key{"busy", Standard}]; !kept {
		t.Error("the bucket in use was swept")
	}
	// Within the interval nothing is scanned, however full.
	c.advance(time.Second)
	m.buckets[Key{"full", Standard}] = &bucket{tokens: 10, at: c.now(), full: c.now()}
	_, _ = m.Take(t.Context(), Key{"busy", Standard}, l)
	if _, kept := m.buckets[Key{"full", Standard}]; !kept {
		t.Error("swept before the interval passed")
	}
}

// TestUnitMemoryConcurrentTake checks that concurrent requests never spend
// one token twice: exactly the burst is admitted.
func TestUnitMemoryConcurrentTake(t *testing.T) {
	m := NewMemory(newClock().now)
	l := Limit{Rate: 1e-9, Burst: 10}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for range 100 {
		wg.Go(func() {
			st, _ := m.Take(context.Background(), Key{"a", Standard}, l)
			if st.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if allowed != 10 {
		t.Errorf("%d requests admitted, want the burst of 10", allowed)
	}
}

func TestUnitValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for name, edit := range map[string]func(*Config){
		"zero rate":     func(c *Config) { c.Standard.Rate = 0 },
		"NaN rate":      func(c *Config) { c.Standard.Rate = math.NaN() },
		"infinite rate": func(c *Config) { c.Expensive.Rate = math.Inf(1) },
		"zero burst":    func(c *Config) { c.Expensive.Burst = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := Defaults()
			edit(&c)
			if err := c.Validate(); !errors.Is(err, errLimit) {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestUnitClientAddr(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	for addr, want := range map[string]string{
		"192.0.2.1:1234":  "192.0.2.1",
		"[2001:db8::1]:1": "2001:db8::1",
		"pipe":            "pipe",
	} {
		r.RemoteAddr = addr
		if got := ClientAddr(r); got != want {
			t.Errorf("ClientAddr(%q) = %q, want %q", addr, got, want)
		}
	}
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, typ problem.Type) {
	t.Helper()
	var p problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem: %s", rec.Body)
	}
	if rec.Code != typ.Status || p.Type != problem.TypePrefix+typ.Slug || p.Title != typ.Title {
		t.Errorf("got %d %+v, want %s", rec.Code, p, typ.Slug)
	}
}

func request(remote, path string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	r.RemoteAddr = remote
	return r
}

// byPath makes /search expensive and everything else what the route says.
func byPath(r *http.Request) Class {
	switch r.URL.Path {
	case "/search":
		return Expensive
	case "/unclassified":
		return ""
	}
	return Standard
}

func TestUnitMiddlewareHeadersAnd429(t *testing.T) {
	c := newClock()
	cfg := Config{Standard: Limit{Rate: 1, Burst: 3}, Expensive: Limit{Rate: 0.5, Burst: 1}}
	h := Middleware(cfg, NewMemory(c.now), ClientAddr, byPath)(http.HandlerFunc(ok))
	serve := func(remote, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, request(remote, path))
		return rec
	}

	rec := serve("192.0.2.1:1", "/stacks")
	if rec.Code != http.StatusOK || rec.Header().Get("X-RateLimit-Limit") != "3" ||
		rec.Header().Get("X-RateLimit-Remaining") != "2" ||
		rec.Header().Get("X-RateLimit-Reset") != "1" || rec.Header().Get("Retry-After") != "" {
		t.Errorf("first standard request: %d %v", rec.Code, rec.Header())
	}

	// The expensive budget runs out first; the standard one still serves.
	if rec := serve("192.0.2.1:1", "/search"); rec.Code != http.StatusOK {
		t.Errorf("first search: %d", rec.Code)
	}
	rec = serve("192.0.2.1:2", "/search")
	wantProblem(t, rec, problem.RateLimited)
	if rec.Header().Get("Retry-After") != "2" || rec.Header().Get("X-RateLimit-Limit") != "1" ||
		rec.Header().Get("X-RateLimit-Remaining") != "0" ||
		rec.Header().Get("X-RateLimit-Reset") != "2" ||
		!strings.Contains(rec.Body.String(), "expensive") {
		t.Errorf("throttled search: %v %s", rec.Header(), rec.Body)
	}
	if rec := serve("192.0.2.1:3", "/unclassified"); rec.Code != http.StatusOK ||
		rec.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Errorf("an unclassified operation did not draw from standard: %v", rec.Header())
	}
	// Another caller has its own buckets.
	if rec := serve("198.51.100.7:1", "/search"); rec.Code != http.StatusOK {
		t.Errorf("another caller's search: %d", rec.Code)
	}
	c.advance(2 * time.Second)
	if rec := serve("192.0.2.1:1", "/search"); rec.Code != http.StatusOK {
		t.Errorf("search after Retry-After: %d", rec.Code)
	}
}

type failing struct{ err error }

var (
	errStore = errors.New("store failed with secret detail")
	// errDown is a store failure sql.Unavailable classified.
	errDown = fmt.Errorf("%w: %w", kernel.ErrUnavailable, errStore)
)

func (f failing) Take(context.Context, Key, Limit) (State, error) {
	return State{}, f.err
}

func (f failing) Disabled(context.Context, string) (bool, error) { return false, f.err }

// TestUnitStoreFailure checks that a store failure answers 500, or 503
// with Retry-After when the store cannot be reached, and never names it.
func TestUnitStoreFailure(t *testing.T) {
	for _, err := range []error{errStore, errDown} {
		for name, mw := range map[string]func(http.Handler) http.Handler{
			"bucket": Middleware(Defaults(), failing{err}, ClientAddr, byPath),
			"switch": Disabled(failing{err}, ClientAddr),
		} {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Error("handler ran")
				})).ServeHTTP(rec, request("192.0.2.1:1", "/"))
				want, retry := problem.InternalError, ""
				if errors.Is(err, kernel.ErrUnavailable) {
					want, retry = problem.ServiceUnavailable, "2"
				}
				wantProblem(t, rec, want)
				if rec.Header().Get("Retry-After") != retry ||
					strings.Contains(rec.Body.String(), "secret detail") {
					t.Errorf("got %d %v %s", rec.Code, rec.Header(), rec.Body)
				}
			})
		}
	}
}

func TestUnitDisabled(t *testing.T) {
	h := Disabled(NewCallers([]string{"192.0.2.1"}), ClientAddr)(http.HandlerFunc(ok))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request("192.0.2.1:1", "/"))
	wantProblem(t, rec, problem.APIDisabled)
	if rec.Header().Get("Retry-After") != "300" {
		t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, request("198.51.100.7:1", "/"))
	if rec.Code != http.StatusOK {
		t.Errorf("another caller got %d", rec.Code)
	}
}
