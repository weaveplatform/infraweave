package kektest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Exchange is one recorded request and its response. Request headers are
// not recorded, so no credential reaches a fixture.
type Exchange struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Body   string            `json:"body,omitempty"`
	Status int               `json:"status"`
	Header map[string]string `json:"header,omitempty"`
	Reply  string            `json:"reply,omitempty"`
}

// recordedHeaders are the response headers a client reads: the body's
// type, and Key Vault's authentication challenge.
var recordedHeaders = []string{"Content-Type", "WWW-Authenticate"}

// uuid matches a tenant or subscription ID, which a fixture never holds.
var uuid = regexp.MustCompile(
	`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`,
)

// canonical is body with a JSON object's keys sorted, so a request
// matches however its client ordered them.
func canonical(body []byte) string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return string(body)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func key(method, path, body string) string { return method + " " + path + " " + body }

// Replay serves the exchanges in file, matching each request by method,
// path and query, and body. A request with no recorded exchange fails t.
func Replay(t *testing.T, file string, tls bool) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	recorded := map[string]Exchange{}
	for _, e := range exchanges {
		recorded[key(e.Method, e.Path, e.Body)] = e
	}
	r := chi.NewRouter()
	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		e, ok := recorded[key(req.Method, req.URL.RequestURI(), canonical(body))]
		if !ok {
			t.Errorf("no recorded exchange for %s %s %s", req.Method, req.URL.RequestURI(), body)
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		for k, v := range e.Header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(e.Status)
		_, _ = io.WriteString(w, e.Reply)
	})
	srv := httptest.NewUnstartedServer(r)
	if tls {
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
	return srv
}

// Recorder is a transport that records each exchange it sends, with
// every pair in Redact replaced and any UUID zeroed, and writes them to
// File when the test ends.
type Recorder struct {
	Base   http.RoundTripper
	File   string
	Redact *strings.Replacer

	mu        sync.Mutex
	exchanges []Exchange
}

// Record returns a Recorder over base that writes file when t ends, and
// pairs, as strings.NewReplacer takes them, name what to redact.
func Record(t *testing.T, base http.RoundTripper, file string, pairs ...string) *Recorder {
	t.Helper()
	rec := &Recorder{Base: base, File: file, Redact: strings.NewReplacer(pairs...)}
	t.Cleanup(func() {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		b, err := json.MarshalIndent(rec.exchanges, "", "  ")
		if err == nil {
			err = os.MkdirAll(filepath.Dir(rec.File), 0o750)
		}
		if err == nil {
			err = os.WriteFile(rec.File, append(b, '\n'), 0o600)
		}
		if err != nil {
			t.Errorf("write %s: %v", rec.File, err)
		}
	})
	return rec
}

func (r *Recorder) redact(s string) string {
	return uuid.ReplaceAllString(r.Redact.Replace(s), "00000000-0000-0000-0000-000000000000")
}

// RoundTrip sends req through Base and records the exchange.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			return nil, err //nolint:wrapcheck // a test transport
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	resp, err := r.Base.RoundTrip(req)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	reply, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	resp.Body = io.NopCloser(bytes.NewReader(reply))
	e := Exchange{
		Method: req.Method,
		Path:   r.redact(req.URL.RequestURI()),
		Body:   r.redact(canonical(body)),
		Status: resp.StatusCode,
		Reply:  r.redact(string(reply)),
	}
	for _, h := range recordedHeaders {
		if v := resp.Header.Get(h); v != "" {
			if e.Header == nil {
				e.Header = map[string]string{}
			}
			e.Header[h] = r.redact(v)
		}
	}
	r.mu.Lock()
	r.exchanges = append(r.exchanges, e)
	r.mu.Unlock()
	return resp, nil
}
