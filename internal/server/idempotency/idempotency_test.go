package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/server/problem"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func byRemote(r *http.Request) string { return r.RemoteAddr }

// notSearch applies the key to every path but /search.
func notSearch(r *http.Request) bool { return r.URL.Path != "/search" }

// counting is a create handler that numbers each run, so a replay is told
// apart from a second run by its body.
type counting struct {
	runs   atomic.Int32
	status int
}

func (c *counting) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := c.runs.Add(1)
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/things/1")
	w.Header().Set("X-Other", "not replayed")
	w.WriteHeader(c.status)
	_ = json.NewEncoder(w).Encode(map[string]any{"run": n, "got": string(body)})
}

func handler(store Store, next http.Handler) http.Handler {
	return Middleware(store, byRemote, notSearch, discard())(next)
}

type req struct {
	method, target, body, remote string
	keys                         []string
}

func send(h http.Handler, q req) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(
		context.Background(),
		q.method,
		q.target,
		strings.NewReader(q.body),
	)
	if q.remote != "" {
		r.RemoteAddr = q.remote
	}
	for _, k := range q.keys {
		r.Header.Add(Header, k)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, typ problem.Type) {
	t.Helper()
	var p problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem: %d %s", rec.Code, rec.Body)
	}
	if rec.Code != typ.Status || p.Type != problem.TypePrefix+typ.Slug {
		t.Errorf("got %d %+v, want %s", rec.Code, p, typ.Slug)
	}
}

func TestUnitReplay(t *testing.T) {
	next := &counting{status: http.StatusCreated}
	h := handler(NewMemory(time.Hour, time.Now), next)
	create := req{
		method: http.MethodPost,
		target: "/things?x=1",
		body:   `{"name":"a"}`,
		keys:   []string{"k1"},
	}

	first := send(h, create)
	second := send(h, create)
	if next.runs.Load() != 1 {
		t.Fatalf("the handler ran %d times", next.runs.Load())
	}
	if first.Header().Get(ReplayedHeader) != "" {
		t.Error("the first response is marked replayed")
	}
	if second.Code != http.StatusCreated || !bytes.Equal(second.Body.Bytes(), first.Body.Bytes()) ||
		second.Header().Get(ReplayedHeader) != "true" ||
		second.Header().Get("Content-Type") != "application/json" ||
		second.Header().Get("Location") != "/things/1" ||
		second.Header().Get("X-Other") != "" {
		t.Errorf("replay: %d %v %s", second.Code, second.Header(), second.Body)
	}

	// PATCH honours keys too.
	patch := req{method: http.MethodPatch, target: "/things/1", body: "{}", keys: []string{"k2"}}
	send(h, patch)
	if rec := send(h, patch); next.runs.Load() != 2 || rec.Header().Get(ReplayedHeader) != "true" {
		t.Errorf("a repeated PATCH ran the handler again: %d runs", next.runs.Load())
	}
	// Keys are per caller.
	other := create
	other.remote = "198.51.100.7:1"
	if send(h, other); next.runs.Load() != 3 {
		t.Error("another caller's identical key was treated as a repeat")
	}
}

func TestUnitReuseWithAnotherRequest(t *testing.T) {
	next := &counting{status: http.StatusCreated}
	h := handler(NewMemory(time.Hour, time.Now), next)
	send(h, req{method: http.MethodPost, target: "/things", body: "a", keys: []string{"k"}})
	for name, q := range map[string]req{
		"body":   {method: http.MethodPost, target: "/things", body: "b", keys: []string{"k"}},
		"query":  {method: http.MethodPost, target: "/things?x=1", body: "a", keys: []string{"k"}},
		"path":   {method: http.MethodPost, target: "/others", body: "a", keys: []string{"k"}},
		"method": {method: http.MethodPatch, target: "/things", body: "a", keys: []string{"k"}},
	} {
		t.Run(name, func(t *testing.T) { wantProblem(t, send(h, q), problem.IdempotencyKeyReuse) })
	}
	if next.runs.Load() != 1 {
		t.Errorf("the handler ran %d times", next.runs.Load())
	}
}

func TestUnitInFlight(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusAccepted)
	})
	h := handler(NewMemory(time.Hour, time.Now), slow)
	q := req{method: http.MethodPost, target: "/runs", keys: []string{"k"}}
	// The first request runs off the test goroutine and reports its status
	// through done rather than t.Fatal.
	done := make(chan int, 1)
	go func() { done <- send(h, q).Code }()
	<-entered
	wantProblem(t, send(h, q), problem.IdempotencyKeyInFlight)
	close(release)
	if code := <-done; code != http.StatusAccepted {
		t.Errorf("first request: %d", code)
	}
	if rec := send(
		h,
		q,
	); rec.Code != http.StatusAccepted ||
		rec.Header().Get(ReplayedHeader) != "true" {
		t.Errorf("after the first finished: %d %v", rec.Code, rec.Header())
	}
}

func TestUnitServerErrorReleases(t *testing.T) {
	next := &counting{status: http.StatusServiceUnavailable}
	h := handler(NewMemory(time.Hour, time.Now), next)
	q := req{method: http.MethodPost, target: "/things", body: "a", keys: []string{"k"}}
	send(h, q)
	send(h, q)
	if next.runs.Load() != 2 {
		t.Errorf("a retry after a 5xx ran the handler %d times in all, want 2", next.runs.Load())
	}
	// Below 500 is stored, a validation failure included.
	next.status = http.StatusBadRequest
	send(h, q)
	if rec := send(h, q); rec.Code != http.StatusBadRequest || next.runs.Load() != 3 {
		t.Errorf("a 400 was not replayed: %d after %d runs", rec.Code, next.runs.Load())
	}
}

func TestUnitPanicReleases(t *testing.T) {
	store := NewMemory(time.Hour, time.Now)
	boom := handler(
		store,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
	)
	q := req{method: http.MethodPost, target: "/things", keys: []string{"k"}}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		send(boom, q)
	}()
	if _, found, _ := store.Get(t.Context(), Key{Caller: "192.0.2.1:1234", Value: "k"}); found {
		t.Error("the key stayed reserved after the handler panicked")
	}
}

func TestUnitIgnoredRequests(t *testing.T) {
	next := &counting{status: http.StatusOK}
	h := handler(NewMemory(time.Hour, time.Now), next)
	for _, q := range []req{
		{method: http.MethodGet, target: "/things", keys: []string{"k"}},
		{method: http.MethodPut, target: "/things/1", keys: []string{"k"}},
		{method: http.MethodDelete, target: "/things/1", keys: []string{"k"}},
		{method: http.MethodPost, target: "/search", keys: []string{"k"}},
		{method: http.MethodPost, target: "/things"},
	} {
		send(h, q)
		if rec := send(h, q); rec.Header().Get(ReplayedHeader) != "" {
			t.Errorf("%s %s was replayed", q.method, q.target)
		}
	}
	if next.runs.Load() != 10 {
		t.Errorf("the handler ran %d times, want every request", next.runs.Load())
	}
}

func TestUnitRejectedBeforeTheStore(t *testing.T) {
	store := &fake{Memory: NewMemory(time.Hour, time.Now)}
	h := handler(store, &counting{status: http.StatusOK})
	for name, tc := range map[string]struct {
		q    req
		want problem.Type
	}{
		"empty key":  {req{method: http.MethodPost, target: "/", keys: []string{""}}, problem.InvalidRequest},
		"long key":   {req{method: http.MethodPost, target: "/", keys: []string{strings.Repeat("k", 256)}}, problem.InvalidRequest},
		"two keys":   {req{method: http.MethodPost, target: "/", keys: []string{"a", "b"}}, problem.InvalidRequest},
		"large body": {req{method: http.MethodPost, target: "/", body: strings.Repeat("x", MaxBody+1), keys: []string{"k"}}, problem.PayloadTooLarge},
	} {
		t.Run(name, func(t *testing.T) { wantProblem(t, send(h, tc.q), tc.want) })
	}
	if store.calls.Load() != 0 {
		t.Errorf("rejected requests reached the store %d times", store.calls.Load())
	}
	if rec := send(
		h,
		req{method: http.MethodPost, target: "/", keys: []string{strings.Repeat("k", 255)}},
	); rec.Code != http.StatusOK {
		t.Errorf("a 255-character key: %d", rec.Code)
	}
}

type brokenBody struct{}

var errBroken = errors.New("connection reset")

func (brokenBody) Read([]byte) (int, error) { return 0, errBroken }

func TestUnitUnreadableBody(t *testing.T) {
	h := handler(NewMemory(time.Hour, time.Now), &counting{status: http.StatusOK})
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", brokenBody{})
	r.Header.Set(Header, "k")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	wantProblem(t, rec, problem.InvalidRequest)
}

// fake is a Memory whose operations can fail or lose a race, and which
// counts its calls.
type fake struct {
	*Memory
	calls                          atomic.Int32
	get, reserve, complete, releas error
	loseRace                       bool
}

var errStore = errors.New("store unavailable")

func (f *fake) Get(ctx context.Context, k Key) (Record, bool, error) {
	f.calls.Add(1)
	if f.get != nil {
		return Record{}, false, f.get
	}
	return f.Memory.Get(ctx, k)
}

func (f *fake) Reserve(ctx context.Context, k Key, fp string) (bool, error) {
	f.calls.Add(1)
	if f.reserve != nil || f.loseRace {
		return false, f.reserve
	}
	return f.Memory.Reserve(ctx, k, fp)
}

func (f *fake) Complete(ctx context.Context, k Key, resp Response) error {
	f.calls.Add(1)
	if f.complete != nil {
		return f.complete
	}
	return f.Memory.Complete(ctx, k, resp)
}

func (f *fake) Release(ctx context.Context, k Key) error {
	f.calls.Add(1)
	if f.releas != nil {
		return f.releas
	}
	return f.Memory.Release(ctx, k)
}

func TestUnitStoreFailures(t *testing.T) {
	q := req{method: http.MethodPost, target: "/", keys: []string{"k"}}
	for name, tc := range map[string]struct {
		store    *fake
		status   int
		wantCode int
		wantRuns int32
		wantLog  string
	}{
		"get":       {&fake{get: errStore}, http.StatusOK, http.StatusInternalServerError, 0, "op=get"},
		"reserve":   {&fake{reserve: errStore}, http.StatusOK, http.StatusInternalServerError, 0, "op=reserve"},
		"lost race": {&fake{loseRace: true}, http.StatusOK, http.StatusConflict, 0, ""},
		"complete":  {&fake{complete: errStore}, http.StatusOK, http.StatusOK, 1, "op=complete"},
		"release":   {&fake{releas: errStore}, http.StatusBadGateway, http.StatusBadGateway, 1, "op=release"},
	} {
		t.Run(name, func(t *testing.T) {
			tc.store.Memory = NewMemory(time.Hour, time.Now)
			var logs bytes.Buffer
			next := &counting{status: tc.status}
			h := Middleware(
				tc.store,
				byRemote,
				notSearch,
				slog.New(slog.NewTextHandler(&logs, nil)),
			)(
				next,
			)
			rec := send(h, q)
			if rec.Code != tc.wantCode || next.runs.Load() != tc.wantRuns {
				t.Errorf("got %d after %d runs: %s", rec.Code, next.runs.Load(), rec.Body)
			}
			if strings.Contains(rec.Body.String(), "unavailable") {
				t.Errorf("the store's error reached the client: %s", rec.Body)
			}
			if !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("logs %q, want %q", logs.String(), tc.wantLog)
			}
		})
	}
}

func TestUnitCapture(t *testing.T) {
	store := NewMemory(time.Hour, time.Now)
	q := req{method: http.MethodPost, target: "/", keys: []string{"k"}}
	// A handler that writes nothing answers 200 with an empty body.
	silent := handler(store, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	send(silent, q)
	if rec := send(
		silent,
		q,
	); rec.Code != http.StatusOK || rec.Header().Get(ReplayedHeader) != "true" ||
		rec.Body.Len() != 0 ||
		rec.Header().Get("Content-Type") != "" {
		t.Errorf("silent replay: %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
	// An interim 1xx is not the stored status, and ResponseController
	// reaches the connection's writer through Unwrap.
	q.keys = []string{"k2"}
	interim := handler(store, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		_ = http.NewResponseController(w).Flush()
		_, _ = w.Write([]byte("done"))
	}))
	send(interim, q)
	rec := send(interim, q)
	if rec.Code != http.StatusOK || rec.Body.String() != "done" {
		t.Errorf("replay after 103: %d %q", rec.Code, rec.Body)
	}
}

// errWriter fails every write, as a connection the client has closed does.
type errWriter struct{ *httptest.ResponseRecorder }

func (errWriter) Write([]byte) (int, error) { return 0, errBroken }

func TestUnitCaptureWriteError(t *testing.T) {
	c := &capture{ResponseWriter: errWriter{httptest.NewRecorder()}}
	if _, err := c.Write([]byte("x")); !errors.Is(err, errBroken) {
		t.Errorf("got %v", err)
	}
}
