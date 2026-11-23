package kektest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// apiVersion is the syntax of Key Vault's api-version query parameter: a
// date, or the major.minor of its earlier versions, either one possibly a
// preview.
var apiVersion = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|\d+\.\d+)(-preview(\.\d+)?)?$`)

// unversioned is uri with the value of its api-version query parameter
// elided and the rest of it as given. The SDK chooses the service API
// version, and a release moves its default without any change in what
// infraweave asks of Key Vault, so a replay matches the parameter's
// presence but not its value; a fixture keeps the version it was
// recorded at. ok is false when the parameter repeats or names no
// version.
func unversioned(uri string) (string, bool) {
	path, query, found := strings.Cut(uri, "?")
	if !found {
		return uri, true
	}
	params := strings.Split(query, "&")
	seen := false
	for i, param := range params {
		name, value, _ := strings.Cut(param, "=")
		if n, err := url.QueryUnescape(name); err != nil || n != "api-version" {
			continue
		}
		v, err := url.QueryUnescape(value)
		if seen || err != nil || !apiVersion.MatchString(v) {
			return uri, false
		}
		seen = true
		params[i] = "api-version=*"
	}
	return path + "?" + strings.Join(params, "&"), true
}

// Replay serves the exchanges in file, matching each request by method,
// path and query, and body, with any api-version matched by its presence
// and syntax but not its value. Requests that match alike get the exchanges
// recorded for them in order, the last one again once they run out: Key
// Vault's challenge answers a request 401 and its identical retry 200. A
// request with no recorded exchange fails t.
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
	recorded := map[string][]Exchange{}
	for _, e := range exchanges {
		path, ok := unversioned(e.Path)
		if !ok {
			t.Fatalf("%s: %s names no single well-formed api-version", file, e.Path)
		}
		k := key(e.Method, path, e.Body)
		recorded[k] = append(recorded[k], e)
	}
	var mu sync.Mutex
	r := chi.NewRouter()
	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		path, ok := unversioned(req.URL.RequestURI())
		if !ok {
			t.Errorf("%s %s names no single well-formed api-version", req.Method, req.URL.RequestURI())
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		k := key(req.Method, path, canonical(body))
		mu.Lock()
		queue, ok := recorded[k]
		var e Exchange
		if ok {
			e = queue[0]
			if len(queue) > 1 {
				recorded[k] = queue[1:]
			}
		}
		mu.Unlock()
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
