package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestUnitRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))
	generated := regexp.MustCompile(`^[A-Z2-7]{26}$`)
	for name, tc := range map[string]struct {
		header string
		keep   bool
	}{
		"accepted":       {"req-1.A_b", true},
		"longest":        {strings.Repeat("a", 128), true},
		"none":           {"", false},
		"too long":       {strings.Repeat("a", 129), false},
		"bad characters": {"a b\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set(RequestIDHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			got := rec.Header().Get(RequestIDHeader)
			if got != seen {
				t.Errorf("header %q, context %q", got, seen)
			}
			if tc.keep && got != tc.header || !tc.keep && !generated.MatchString(got) {
				t.Errorf("sent %q, got %q", tc.header, got)
			}
		})
	}
	if RequestIDFrom(t.Context()) != "" {
		t.Error("an ID outside a request")
	}
}

func TestUnitAccessLog(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	r := NewRouter(discard(), AccessLog(log))
	r.Get("/things/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("hello"))
	})
	r.Get("/empty", func(http.ResponseWriter, *http.Request) {})
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	for _, target := range []string{"/things/42", "/empty", "/boom"} {
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			target,
			strings.NewReader("secret body"),
		)
		req.Header.Set("User-Agent", "test/1")
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	raw := bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n"))
	lines := make([]map[string]any, 0, len(raw))
	for _, l := range raw {
		var m map[string]any
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, m)
	}
	want := []struct {
		route  string
		status float64
		bytes  float64
	}{{"/things/{id}", 202, 5}, {"/empty", 200, 0}, {"/boom", 500, 0}}
	if len(lines) != len(want) {
		t.Fatalf("%d lines: %s", len(lines), logs.String())
	}
	for i, w := range want {
		l := lines[i]
		if l["msg"] != "request" || l["method"] != "GET" || l["route"] != w.route ||
			l["status"] != w.status || l["bytes"] != w.bytes || l["user_agent"] != "test/1" ||
			l["remote_addr"] == "" || l["duration_ms"] == nil {
			t.Errorf("line %d: %v", i, l)
		}
	}
	if strings.Contains(logs.String(), "secret body") {
		t.Error("a body was logged")
	}
	// Outside a chi router there is no route.
	logs.Reset()
	AccessLog(log)(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if !strings.Contains(logs.String(), `"route":""`) {
		t.Errorf("no router: %s", logs.String())
	}
}
