package observe

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
)

// Check reports whether one dependency of the process is as it should be.
type Check func(ctx context.Context) error

// Each check runs for at most checkTimeout, a timeout counting as a
// failure, and its result is kept for cacheFor, so probes from several
// load balancers do not load the database (decision 0017).
const (
	checkTimeout = 2 * time.Second
	cacheFor     = time.Second
)

// Health holds a process's startup and readiness checks, which each
// process mode adds to.
type Health struct {
	now func() time.Time

	mu      sync.Mutex
	startup []named
	ready   []named
	cache   map[string]result

	started  atomic.Bool
	stopping atomic.Bool
}

type named struct {
	// key is the check's cache key, its kind and name; a startup and a
	// readiness check may share a name.
	key, name string
	check     Check
}

type result struct {
	err error
	at  time.Time
}

// NewHealth has no checks yet; now is the clock its cache runs by.
func NewHealth(now func() time.Time) *Health {
	return &Health{now: now, cache: map[string]result{}}
}

// Startup adds a check that must pass once before /startupz answers 200.
func (h *Health) Startup(name string, c Check) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.startup = append(h.startup, named{"startup/" + name, name, c})
}

// Ready adds a check that must pass for /readyz to answer 200.
func (h *Health) Ready(name string, c Check) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = append(h.ready, named{"ready/" + name, name, c})
}

// Stop makes /readyz answer 503 from now on, as shutdown begins.
func (h *Health) Stop() { h.stopping.Store(true) }

// report is the verbose form of a probe's answer.
type report struct {
	Status string            `json:"status"`
	Checks map[string]status `json:"checks"`
}

type status struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// run runs checks concurrently, each from its cache when the cached result
// is fresh.
func (h *Health) run(ctx context.Context, checks []named) report {
	results := make([]error, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Go(func() { results[i] = h.result(ctx, c) })
	}
	wg.Wait()
	rep := report{Status: "ok", Checks: map[string]status{}}
	for i, c := range checks {
		s := status{Status: "ok"}
		if results[i] != nil {
			s = status{Status: "fail", Error: results[i].Error()}
			rep.Status = "fail"
		}
		rep.Checks[c.name] = s
	}
	return rep
}

func (h *Health) result(ctx context.Context, c named) error {
	h.mu.Lock()
	r, ok := h.cache[c.key]
	h.mu.Unlock()
	if ok && h.now().Sub(r.at) < cacheFor {
		return r.err
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	err := c.check(ctx)
	h.mu.Lock()
	h.cache[c.key] = result{err: err, at: h.now()}
	h.mu.Unlock()
	return err
}

func (h *Health) checks(startup bool) []named {
	h.mu.Lock()
	defer h.mu.Unlock()
	if startup {
		return append([]named(nil), h.startup...)
	}
	return append([]named(nil), h.ready...)
}

// startupReport is /startupz's answer: once every startup check has passed
// it passes for good.
func (h *Health) startupReport(ctx context.Context) report {
	if h.started.Load() {
		return report{Status: "ok", Checks: map[string]status{}}
	}
	rep := h.run(ctx, h.checks(true))
	if rep.Status == "ok" {
		h.started.Store(true)
	}
	return rep
}

// readyReport is /readyz's answer.
func (h *Health) readyReport(ctx context.Context) report {
	if h.stopping.Load() {
		return report{Status: "fail", Checks: map[string]status{
			"shutdown": {Status: "fail", Error: "shutting down"},
		}}
	}
	return h.run(ctx, h.checks(false))
}

// Probes is the admin listener's handler (decision 0017): GET /livez
// answers 200 while the process serves HTTP, /startupz and /readyz answer
// 200 or 503 from h's checks, as ok or fail, or as JSON with ?verbose.
// Every other path or method is a problem detail.
func Probes(log *slog.Logger, h *Health) http.Handler {
	r := middleware.NewRouter(log)
	r.Get("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	r.Get("/startupz", func(w http.ResponseWriter, req *http.Request) {
		write(w, req, h.startupReport(req.Context()))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		write(w, req, h.readyReport(req.Context()))
	})
	return r
}

func write(w http.ResponseWriter, r *http.Request, rep report) {
	code := http.StatusOK
	if rep.Status != "ok" {
		code = http.StatusServiceUnavailable
	}
	if r.URL.Query().Has("verbose") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(rep)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(rep.Status + "\n"))
}
