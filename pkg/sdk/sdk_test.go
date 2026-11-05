package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/pkg/client/api"
)

// inMemory serves requests with a chi router in the calling goroutine. A
// real socket would stall synctest's virtual clock, which advances only
// once every goroutine of the bubble is durably blocked.
type inMemory struct{ h http.Handler }

func (m inMemory) Do(req *http.Request) (*http.Response, error) {
	// A server never hands a handler a nil body.
	if req.Body == nil {
		req.Body = http.NoBody
	}
	rec := httptest.NewRecorder()
	m.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// attempts records what each attempt of a call carried.
type attempts struct {
	mu   sync.Mutex
	keys []string
	auth []string
	body []string
	at   []time.Time
}

func (a *attempts) record(r *http.Request) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	a.keys = append(a.keys, r.Header.Get(IdempotencyKeyHeader))
	a.auth = append(a.auth, r.Header.Get("Authorization"))
	a.body = append(a.body, string(b))
	a.at = append(a.at, time.Now())
	return len(a.keys)
}

func writeProblem(w http.ResponseWriter, status int, slug string, header map[string]string) {
	for k, v := range header {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type: TypePrefix + slug, Title: slug, Status: status, Detail: "d", Instance: "req-1",
	})
}

// faulty answers each attempt with the next of answers, then 201.
func faulty(a *attempts, answers ...func(w http.ResponseWriter)) http.Handler {
	r := chi.NewRouter()
	r.HandleFunc("/api/v1/things", func(w http.ResponseWriter, req *http.Request) {
		n := a.record(req)
		if n <= len(answers) {
			answers[n-1](w)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	return r
}

func client(t *testing.T, h http.Handler, opts ...Option) *Client {
	t.Helper()
	c, err := New(
		"http://infraweave.test/",
		append([]Option{WithHTTPClient(inMemory{h})}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func problemAnswer(status int, slug string, header map[string]string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { writeProblem(w, status, slug, header) }
}

func TestUnitSDKRetriedPostKeepsItsKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &attempts{}
		c := client(t, faulty(a,
			problemAnswer(503, "service-unavailable", map[string]string{"Retry-After": "1"}),
			problemAnswer(429, "rate-limited", map[string]string{"Retry-After": "2"}),
			problemAnswer(500, "internal-error", nil),
			problemAnswer(409, "idempotency-key-in-flight", nil),
		), WithToken("tok"))
		start := time.Now()
		resp, err := c.Do(
			t.Context(),
			http.MethodPost,
			"/api/v1/things",
			map[string]string{"name": "n"},
		)
		if err != nil || resp.Status != http.StatusCreated {
			t.Fatalf("Do = %v, %v", resp, err)
		}
		if len(a.keys) != 5 || a.keys[0] == "" ||
			slices.Compact(slices.Clone(a.keys))[0] != a.keys[0] ||
			len(slices.Compact(slices.Clone(a.keys))) != 1 {
			t.Errorf("keys across attempts = %q, want one key five times", a.keys)
		}
		for i, b := range a.body {
			if b != `{"name":"n"}` || a.auth[i] != "Bearer tok" {
				t.Errorf("attempt %d sent %q with %q", i+1, b, a.auth[i])
			}
		}
		// Retry-After 1 and 2, then Backoff after the 500 and twice it
		// after the 409.
		want := []time.Duration{
			0,
			time.Second,
			3 * time.Second,
			3*time.Second + 200*time.Millisecond,
			3*time.Second + 600*time.Millisecond,
		}
		for i, at := range a.at {
			if got := at.Sub(start); got != want[i] {
				t.Errorf("attempt %d at %s, want %s", i+1, got, want[i])
			}
		}
	})
}

func TestUnitSDKRetryRules(t *testing.T) {
	date := func() string { return time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat) }
	for _, tc := range []struct {
		name     string
		method   string
		answer   func() func(w http.ResponseWriter)
		retry    Retry
		attempts int
		elapsed  time.Duration
		want     any
	}{
		{
			"429 on a GET waits Retry-After", http.MethodGet,
			func() func(http.ResponseWriter) {
				return problemAnswer(429, "rate-limited", map[string]string{"Retry-After": "1"})
			},
			DefaultRetry, 2, time.Second, nil,
		},
		{
			"Retry-After as an HTTP date", http.MethodGet,
			func() func(http.ResponseWriter) {
				return problemAnswer(429, "rate-limited", map[string]string{"Retry-After": date()})
			},
			DefaultRetry, 2, 3 * time.Second, nil,
		},
		{
			"500 on a GET is returned", http.MethodGet,
			func() func(http.ResponseWriter) { return problemAnswer(500, "internal-error", nil) },
			DefaultRetry, 1, 0,
			InternalError{},
		},
		{
			"503 without Retry-After on a GET is returned", http.MethodGet,
			func() func(http.ResponseWriter) { return problemAnswer(503, "service-unavailable", nil) },
			DefaultRetry, 1, 0,
			ServiceUnavailable{},
		},
		{
			"501 on a POST is returned", http.MethodPost,
			func() func(http.ResponseWriter) { return problemAnswer(501, "not-implemented", nil) },
			DefaultRetry, 1, 0,
			NotImplemented{},
		},
		{
			"a wait beyond MaxWait is returned", http.MethodGet,
			func() func(http.ResponseWriter) {
				return problemAnswer(429, "rate-limited", map[string]string{"Retry-After": "120"})
			},
			DefaultRetry, 1, 0,
			RateLimited{},
		},
		{
			"a keyed 503 honours Retry-After over Backoff", http.MethodPatch,
			func() func(http.ResponseWriter) {
				return problemAnswer(503, "api-disabled", map[string]string{"Retry-After": "5"})
			},
			DefaultRetry, 2, 5 * time.Second, nil,
		},
		{
			"a keyed 500 waits the Retry-After it names", http.MethodPost,
			func() func(http.ResponseWriter) {
				return problemAnswer(500, "internal-error", map[string]string{"Retry-After": "2"})
			},
			DefaultRetry, 2, 2 * time.Second, nil,
		},
		{
			"an unparsable Retry-After names no wait", http.MethodGet,
			func() func(http.ResponseWriter) {
				return problemAnswer(429, "rate-limited", map[string]string{"Retry-After": "soon"})
			},
			DefaultRetry, 1, 0,
			RateLimited{},
		},
		{
			"NoRetry sends once", http.MethodGet,
			func() func(http.ResponseWriter) {
				return problemAnswer(429, "rate-limited", map[string]string{"Retry-After": "1"})
			},
			NoRetry, 1, 0,
			RateLimited{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := &attempts{}
				c := client(t, faulty(a, tc.answer()), WithRetry(tc.retry))
				start := time.Now()
				_, err := c.Do(t.Context(), tc.method, "/api/v1/things", nil)
				if len(a.keys) != tc.attempts || time.Since(start) != tc.elapsed {
					t.Errorf(
						"%d attempts over %s, want %d over %s",
						len(a.keys),
						time.Since(start),
						tc.attempts,
						tc.elapsed,
					)
				}
				switch {
				case tc.want == nil && err != nil:
					t.Errorf("Do = %v", err)
				case tc.want != nil && reflect.TypeOf(err) != reflect.TypeOf(tc.want):
					t.Errorf("Do = %T %v, want %T", err, err, tc.want)
				}
			})
		})
	}
}

func TestUnitSDKRetryBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &attempts{}
		always := problemAnswer(429, "rate-limited", map[string]string{"Retry-After": "1"})
		c := client(
			t,
			faulty(a, always, always, always, always, always, always),
			WithRetry(Retry{Attempts: 3, MaxWait: time.Minute}),
		)
		_, err := c.Do(t.Context(), http.MethodGet, "/api/v1/things", nil)
		if !errors.As(err, &RateLimited{}) || len(a.keys) != 3 {
			t.Errorf("Do = %v after %d attempts, want rate-limited after 3", err, len(a.keys))
		}
		if a.keys[0] != "" {
			t.Errorf("a GET carried Idempotency-Key %q", a.keys[0])
		}
	})
	synctest.Test(t, func(t *testing.T) {
		a := &attempts{}
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		c := client(
			t,
			faulty(a, problemAnswer(429, "rate-limited", map[string]string{"Retry-After": "1"})),
		)
		if _, err := c.Do(
			ctx,
			http.MethodGet,
			"/api/v1/things",
			nil,
		); !errors.Is(
			err,
			context.DeadlineExceeded,
		) {
			t.Errorf("Do = %v, want the context's deadline", err)
		}
	})
}

func TestUnitSDKKeepsCallerKeyAndRereadsBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &attempts{}
		c := client(
			t,
			faulty(
				a,
				problemAnswer(503, "service-unavailable", map[string]string{"Retry-After": "1"}),
			),
		)
		// A body with no GetBody, as a caller's own reader has.
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"http://infraweave.test/api/v1/things",
			struct{ io.Reader }{strings.NewReader("payload")},
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(IdempotencyKeyHeader, "mine")
		resp, err := transport{c}.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("Do answered %d", resp.StatusCode)
		}
		if !slices.Equal(a.keys, []string{"mine", "mine"}) ||
			!slices.Equal(a.body, []string{"payload", "payload"}) {
			t.Errorf("keys %q, bodies %q", a.keys, a.body)
		}
	})
}

type failing struct{ err error }

func (f failing) Do(*http.Request) (*http.Response, error) { return nil, f.err }

type badBody struct{}

func (badBody) Read([]byte) (int, error) { return 0, errors.New("unreadable") }

func TestUnitSDKDoErrors(t *testing.T) {
	down := errors.New("connection refused")
	c, err := New("http://infraweave.test", WithHTTPClient(failing{down}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(t.Context(), http.MethodGet, "/api/v1/things", nil); !errors.Is(err, down) {
		t.Errorf("a failing transport: %v", err)
	}
	if _, err := c.Do(t.Context(), http.MethodPost, "/x", func() {}); err == nil {
		t.Error("an unencodable body was sent")
	}
	if _, err := c.Do(t.Context(), "BAD METHOD", "/x", nil); err == nil {
		t.Error("an invalid method was sent")
	}
	req, _ := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"http://infraweave.test/x",
		badBody{},
	)
	resp, err := transport{c}.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("an unreadable body: %v", err)
	}
}

// A body that stops arriving ends with the caller's context, not a hang.
func TestUnitSDKSlowBody(t *testing.T) {
	release := make(chan struct{})
	r := chi.NewRouter()
	r.Get("/api/v1/stacks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[`))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(r)
	t.Cleanup(func() { close(release); srv.Close() })
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.Do(
		ctx,
		http.MethodGet,
		"/api/v1/stacks",
		nil,
	); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Errorf("Do = %v, want the context's deadline", err)
	}
}

func TestUnitSDKCheck(t *testing.T) {
	check := func(status int, ct, body string) error {
		resp := &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {ct}},
			Body:       http.NoBody,
		}
		defer resp.Body.Close()
		return Check(resp, []byte(body))
	}
	problem := func(status int, typ string) string {
		b, _ := json.Marshal(
			Problem{Type: typ, Title: "t", Status: status, Detail: "why", Instance: "req-9"},
		)
		return string(b)
	}
	err := check(
		404,
		"application/problem+json; charset=utf-8",
		problem(404, TypePrefix+"not-found"),
	)
	var nf NotFound
	if !errors.As(err, &nf) || nf.Detail != "why" || nf.Instance != "req-9" ||
		nf.Slug() != "not-found" ||
		err.Error() != "t: why" {
		t.Errorf("Check = %#v", err)
	}
	if p, ok := AsProblem(err); !ok || p.Status != 404 {
		t.Errorf("AsProblem = %v, %v", p, ok)
	}
	if errors.As(err, &Forbidden{}) {
		t.Error("a not-found problem is Forbidden")
	}
	var generic Problem
	if err := check(
		418,
		"application/problem+json",
		problem(418, "about:blank"),
	); !errors.As(
		err,
		&generic,
	) ||
		generic.Slug() != "" {
		t.Errorf("an unregistered type: %#v", err)
	}
	if p := (Problem{Title: "only"}); p.Error() != "only" {
		t.Errorf("a problem with no detail reads %q", p.Error())
	}
	for name, a := range map[string][2]string{
		"malformed problem": {"application/problem+json", "{"},
		"status mismatch":   {"application/problem+json", problem(400, TypePrefix+"not-found")},
		"not a problem":     {"text/html", "<h1>Bad Gateway</h1>"},
	} {
		err := check(502, a[0], a[1])
		var he *HTTPError
		if !errors.As(err, &he) || he.Status != 502 ||
			he.Error() != "Bad Gateway (502), not a problem detail" {
			t.Errorf("%s: %v", name, err)
		}
		if _, ok := AsProblem(err); ok {
			t.Errorf("%s reads as a problem", name)
		}
	}
	if err := check(204, "", ""); err != nil {
		t.Errorf("a 204: %v", err)
	}
}

// TestUnitSDKProblemSlugs holds the typed errors to docs/data/problems.json:
// one per slug, named after it.
func TestUnitSDKProblemSlugs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "data", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Records []struct {
			ID string `json:"id"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(reg.Records))
	for _, r := range reg.Records {
		want = append(want, r.ID)
	}
	slices.Sort(want)
	got := Slugs()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("typed errors %q, register %q", got, want)
	}
	for _, s := range got {
		name := ""
		for p := range strings.SplitSeq(s, "-") {
			if p == "api" {
				name += "API"
				continue
			}
			name += strings.ToUpper(p[:1]) + p[1:]
		}
		if typ := reflect.TypeOf(bySlug[s](Problem{})); typ.Name() != name {
			t.Errorf("%s is typed %s, want %s", s, typ.Name(), name)
		}
	}
}

func TestUnitSDKIterate(t *testing.T) {
	pages := map[string]struct {
		items []string
		next  string
	}{"": {[]string{"a", "b"}, "p2"}, "p2": {[]string{"c"}, "p3"}, "p3": {[]string{"d"}, ""}}
	fetched := 0
	r := chi.NewRouter()
	r.Get("/api/v1/stacks", func(w http.ResponseWriter, req *http.Request) {
		fetched++
		p := pages[req.URL.Query().Get("cursor")]
		page := api.StackPage{Items: []api.Stack{}}
		for _, id := range p.items {
			page.Items = append(page.Items, api.Stack{Id: id})
		}
		if p.next != "" || req.URL.Query().Get("cursor") == "p3" {
			page.NextCursor = &p.next
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	})
	c := client(t, r)
	stacks := func(ctx context.Context, cursor *string) ([]api.Stack, *string, error) {
		resp, err := c.API.StacksWithResponse(ctx, &api.StacksParams{Cursor: cursor})
		if err == nil {
			err = Check(resp.HTTPResponse, resp.Body)
		}
		if err != nil {
			return nil, nil, err
		}
		return resp.JSON200.Items, resp.JSON200.NextCursor, nil
	}
	var got []string
	for s, err := range Iterate(t.Context(), stacks) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, s.Id)
	}
	if !slices.Equal(got, []string{"a", "b", "c", "d"}) || fetched != 3 {
		t.Errorf("Iterate yielded %q over %d pages, want a b c d over 3", got, fetched)
	}
	fetched = 0
	for range Iterate(t.Context(), stacks) {
		break
	}
	if fetched != 1 {
		t.Errorf("breaking on the first item fetched %d pages", fetched)
	}
	boom := errors.New("boom")
	n := 0
	for _, err := range Iterate(t.Context(), func(context.Context, *string) ([]int, *string, error) { return nil, nil, boom }) {
		n++
		if !errors.Is(err, boom) {
			t.Errorf("Iterate yielded %v", err)
		}
	}
	if n != 1 {
		t.Errorf("an error was yielded %d times", n)
	}
	last := ""
	for v := range Iterate(t.Context(), func(context.Context, *string) ([]int, *string, error) { return []int{1}, &last, nil }) {
		if v != 1 {
			t.Errorf("yielded %d", v)
		}
	}
}

func TestUnitSDKDoHeaders(t *testing.T) {
	var got http.Header
	r := chi.NewRouter()
	r.Post("/api/v1/things", func(w http.ResponseWriter, req *http.Request) {
		got = req.Header.Clone()
		w.WriteHeader(http.StatusCreated)
	})
	c := client(t, r)
	if _, err := c.Do(t.Context(), http.MethodPost, "/api/v1/things", nil,
		Header{IdempotencyKeyHeader, "caller-key"}, Header{"X-Test", "yes"}); err != nil {
		t.Fatal(err)
	}
	if got.Get(IdempotencyKeyHeader) != "caller-key" || got.Get("X-Test") != "yes" {
		t.Errorf("sent %v", got)
	}
}
