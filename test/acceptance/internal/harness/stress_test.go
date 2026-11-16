package harness

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestUnitParseStress checks each stress EnvStress names, a value out of
// its range and an unknown stress, and the GOMAXPROCS a CPU cap gives.
func TestUnitParseStress(t *testing.T) {
	got, err := ParseStress(" refuse=0.2, latency=500ms ,cpus=0.5,")
	if err != nil || got != (Stress{Refuse: 0.2, Latency: 500 * time.Millisecond, CPUs: 0.5}) {
		t.Fatalf("ParseStress = %+v, %v", got, err)
	}
	if got, err := ParseStress(""); err != nil || got != (Stress{}) || got.store() ||
		got.procs() != "" {
		t.Errorf("no stress = %+v, %v", got, err)
	}
	for _, bad := range []string{
		"refuse=1.5", "refuse=-0.1", "refuse=most", "latency=-1s", "latency=slow",
		"cpus=-1", "cpus=+Inf", "skew=30m", "refuse",
	} {
		if _, err := ParseStress(bad); !errors.Is(err, ErrStress) {
			t.Errorf("ParseStress(%q) = %v, want ErrStress", bad, err)
		}
	}
	for cpus, want := range map[float64]string{0.5: "1", 1: "1", 2.5: "3"} {
		if got := (Stress{CPUs: cpus}).procs(); got != want {
			t.Errorf("procs of %v CPUs = %q, want %q", cpus, got, want)
		}
	}
	if !(Stress{Latency: time.Millisecond}).store() || !(Stress{Refuse: 0.1}).store() ||
		(Stress{CPUs: 1}).store() {
		t.Error("store() does not follow refuse and latency alone")
	}
}

// store is an object store a relay passes requests on to, recording each
// one's method, path and Host.
type store struct {
	mu   sync.Mutex
	seen []string
	srv  *httptest.Server
}

func newStore(t *testing.T) *store {
	t.Helper()
	s := &store{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		s.mu.Lock()
		s.seen = append(s.seen, r.Method+" "+r.URL.Path+" "+r.Host)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *store) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func startStoreRelay(t *testing.T, s *store, stress Stress) *StoreRelay {
	t.Helper()
	r, err := StartStoreRelay(t.Context(), s.srv.URL, stress)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r
}

func send(ctx context.Context, t *testing.T, method, url string) (int, string) {
	t.Helper()
	var body io.Reader
	if method == http.MethodPut {
		body = strings.NewReader("line\n")
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(answer)
}

// TestUnitStoreRelay checks that a refusal share of 1 answers every
// signed upload with a skewed store's 403 and passes the rest on with
// their Host, that a share of 0 passes every upload, that a share of 0.2
// refuses every fifth, and that the relay records each call.
func TestUnitStoreRelay(t *testing.T) {
	s := newStore(t)
	r := startStoreRelay(t, s, Stress{Refuse: 1})
	signed := r.URL + "/bucket/logs/0?X-Amz-Signature=abc"
	code, body := send(t.Context(), t, http.MethodPut, signed)
	if code != http.StatusForbidden || !strings.Contains(body, "<Code>RequestTimeTooSkewed</Code>") {
		t.Errorf("a signed upload under refuse=1 = %d %s", code, body)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if code, body := send(t.Context(), t, method, r.URL+"/bucket/logs/0"); code != http.StatusOK {
			t.Errorf("an unsigned %s = %d %s", method, code, body)
		}
	}
	if code, _ := send(t.Context(), t, http.MethodGet, signed); code != http.StatusOK {
		t.Errorf("a signed read = %d", code)
	}
	host := strings.TrimPrefix(r.URL, "http://")
	want := []string{
		"GET /bucket/logs/0 " + host, "PUT /bucket/logs/0 " + host, "GET /bucket/logs/0 " + host,
	}
	if got := s.requests(); strings.Join(got, ";") != strings.Join(want, ";") {
		t.Errorf("the store saw %q, want %q", got, want)
	}
	calls := r.Calls()
	if len(calls) != 4 || calls[0] != (Call{
		Method: http.MethodPut, Path: "/bucket/logs/0", Status: http.StatusForbidden,
	}) ||
		calls[3].Status != http.StatusOK {
		t.Errorf("calls = %+v", calls)
	}

	open := startStoreRelay(t, s, Stress{})
	if code, _ := send(t.Context(), t, http.MethodPut,
		open.URL+"/bucket/logs/1?X-Amz-Signature=abc"); code != http.StatusOK {
		t.Errorf("a signed upload under refuse=0 = %d", code)
	}

	fifth := startStoreRelay(t, s, Stress{Refuse: 0.2})
	var refused []int
	for i := range 10 {
		if code, _ := send(t.Context(), t, http.MethodPut,
			fifth.URL+"/bucket/logs/2?X-Amz-Signature=abc"); code == http.StatusForbidden {
			refused = append(refused, i)
		}
	}
	if len(refused) != 2 || refused[0] != 4 || refused[1] != 9 {
		t.Errorf("refuse=0.2 refused uploads %v of 10, want 4 and 9", refused)
	}
}

// TestUnitStoreRelayLatency checks that the relay holds each request for
// the latency, and passes nothing on for a caller that gave up first. A
// caller's giving up reaches the relay once it has read the request's
// body, so the read it gives up on has none.
func TestUnitStoreRelayLatency(t *testing.T) {
	s := newStore(t)
	r := startStoreRelay(t, s, Stress{Latency: 200 * time.Millisecond})
	start := time.Now()
	if code, _ := send(t.Context(), t, http.MethodGet, r.URL+"/bucket/a"); code != http.StatusOK {
		t.Fatalf("a held read = %d", code)
	}
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Errorf("a read took %v, under the latency", took)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if code, _ := send(ctx, t, http.MethodGet, r.URL+"/bucket/b"); code != 0 {
		t.Errorf("a read given up = %d", code)
	}
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := s.requests(); len(got) != 1 {
		t.Errorf("the store saw %q, want the first read alone", got)
	}
}
