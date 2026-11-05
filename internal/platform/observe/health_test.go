package observe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a settable time source, so the cache is tested without waiting.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fake is a check whose result the test sets and whose calls it counts.
type fake struct {
	err   atomic.Pointer[error]
	calls atomic.Int32
}

func (f *fake) set(err error) { f.err.Store(&err) }

func (f *fake) check(context.Context) error {
	f.calls.Add(1)
	if p := f.err.Load(); p != nil {
		return *p
	}
	return nil
}

func verbose(t *testing.T, h http.Handler, path string) (int, report) {
	t.Helper()
	rec := serve(t, h, http.MethodGet, path+"?verbose")
	var rep report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("%s?verbose: %v: %s", path, err, rec.Body)
	}
	return rec.Code, rep
}

func TestUnitHealth(t *testing.T) {
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	h := NewHealth(c.now)
	schema, db := &fake{}, &fake{}
	h.Startup("schema", schema.check)
	h.Ready("postgres", db.check)
	probes := Probes(discard(), h)

	schema.set(errors.New("2 migrations pending"))
	code, rep := verbose(t, probes, "/startupz")
	if code != http.StatusServiceUnavailable || rep.Status != "fail" ||
		rep.Checks["schema"] != (status{"fail", "2 migrations pending"}) {
		t.Errorf("startup before the schema: %d %+v", code, rep)
	}
	if rec := serve(
		t,
		probes,
		http.MethodGet,
		"/startupz",
	); rec.Code != http.StatusServiceUnavailable ||
		rec.Body.String() != "fail\n" {
		t.Errorf("terse startup: %d %q", rec.Code, rec.Body)
	}
	if schema.calls.Load() != 1 {
		t.Errorf("a cached result ran the check again: %d calls", schema.calls.Load())
	}
	schema.set(nil)
	c.advance(cacheFor)
	if code, _ := verbose(t, probes, "/startupz"); code != http.StatusOK {
		t.Errorf("startup after the schema: %d", code)
	}
	// Startup passes for good once it has passed.
	schema.set(errors.New("gone"))
	c.advance(cacheFor)
	if rec := serve(
		t,
		probes,
		http.MethodGet,
		"/startupz",
	); rec.Code != http.StatusOK ||
		rec.Body.String() != "ok\n" {
		t.Errorf("startup after it passed: %d %q", rec.Code, rec.Body)
	}

	if code, rep := verbose(
		t,
		probes,
		"/readyz",
	); code != http.StatusOK ||
		rep.Checks["postgres"].Status != "ok" {
		t.Errorf("ready: %d %+v", code, rep)
	}
	db.set(errors.New("connection refused"))
	if code, _ := verbose(t, probes, "/readyz"); code != http.StatusOK {
		t.Errorf("a cached pass was not used: %d", code)
	}
	c.advance(cacheFor)
	if code, rep := verbose(t, probes, "/readyz"); code != http.StatusServiceUnavailable ||
		rep.Checks["postgres"].Error != "connection refused" {
		t.Errorf("not ready: %d %+v", code, rep)
	}
	db.set(nil)
	c.advance(cacheFor)
	if code, _ := verbose(t, probes, "/readyz"); code != http.StatusOK {
		t.Errorf("ready again: %d", code)
	}
	h.Stop()
	if code, rep := verbose(t, probes, "/readyz"); code != http.StatusServiceUnavailable ||
		rep.Checks["shutdown"].Status != "fail" {
		t.Errorf("after Stop: %d %+v", code, rep)
	}
	if rec := serve(t, probes, http.MethodGet, "/livez"); rec.Code != http.StatusOK {
		t.Errorf("/livez after Stop: %d", rec.Code)
	}
}

// TestUnitHealthTimeout checks that a check that hangs fails after the
// check timeout rather than holding the probe.
func TestUnitHealthTimeout(t *testing.T) {
	h := NewHealth(time.Now)
	h.Ready("hangs", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if rep := h.readyReport(ctx); rep.Status != "fail" ||
		rep.Checks["hangs"].Error != context.Canceled.Error() {
		t.Errorf("%+v", rep)
	}
}
