package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
)

func lines(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	raw := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	out := make([]map[string]any, 0, len(raw))
	for _, l := range raw {
		var m map[string]any
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatalf("not one JSON object per line: %v: %s", err, l)
		}
		out = append(out, m)
	}
	return out
}

func TestUnitNewLogger(t *testing.T) {
	var b bytes.Buffer
	log, lv, err := NewLogger(&b, LogDefaults(), "serve,scheduler")
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("hidden")
	log.Info("plain")
	// A line inside a request carries its ID.
	h := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		log.InfoContext(r.Context(), "inside")
		log.InfoContext(r.Context(), "named", "request_id", "mine")
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(middleware.RequestIDHeader, "req-1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	lv.Set(slog.LevelDebug)
	log.Debug("shown")

	got := lines(t, &b)
	if len(got) != 4 {
		t.Fatalf("%d lines: %s", len(got), b.String())
	}
	for _, l := range got {
		ts, _ := l["time"].(string)
		parsed, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || parsed.Location() != time.UTC || !strings.HasSuffix(ts, "Z") ||
			l["service"] != Service || l["mode"] != "serve,scheduler" || l["version"] != buildinfo.Version() ||
			l["level"] == nil || l["msg"] == nil {
			t.Errorf("standard fields: %v", l)
		}
	}
	if got[0]["request_id"] != nil || got[1]["request_id"] != "req-1" ||
		got[2]["request_id"] != "mine" ||
		got[3]["msg"] != "shown" {
		t.Errorf("request IDs and levels: %v", got)
	}
	if strings.Count(b.String(), `"request_id"`) != 2 {
		t.Errorf("a named request_id was duplicated: %s", b.String())
	}

	b.Reset()
	text, _, err := NewLogger(&b, LogConfig{Format: "text", Level: "warn"}, "serve")
	if err != nil {
		t.Fatal(err)
	}
	text.Info("hidden")
	text.Warn("shown", "password", "hunter2")
	if out := b.String(); !strings.Contains(out, "msg=shown") || strings.Contains(out, "hidden") ||
		strings.Contains(out, "hunter2") || !strings.Contains(out, "service=infraweave") {
		t.Errorf("text: %s", out)
	}
}

func TestUnitLogConfig(t *testing.T) {
	if err := LogDefaults().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"debug", "info", "warn", "error"} {
		if _, err := ParseLevel(l); err != nil {
			t.Error(l, err)
		}
	}
	err := LogConfig{Format: "xml", Level: "loud"}.Validate()
	if !errors.Is(err, ErrLogConfig) || !strings.Contains(err.Error(), "log.format") ||
		!strings.Contains(err.Error(), "log.level") {
		t.Errorf("bad settings: %v", err)
	}
	if _, _, err := NewLogger(
		&bytes.Buffer{},
		LogConfig{Format: "json", Level: "loud"},
		"serve",
	); !errors.Is(
		err,
		ErrLogConfig,
	) {
		t.Errorf("NewLogger with a bad level: %v", err)
	}
}

// TestUnitRedactHandler checks each rule of decision 0017's redaction
// table.
func TestUnitRedactHandler(t *testing.T) {
	var b bytes.Buffer
	log := slog.New(Redact(slog.NewJSONHandler(&b, nil))).With("api_token", "base-attr-value")
	hdr := http.Header{
		"Authorization":   {"Bearer abc"},
		"Cookie":          {"session=abc"},
		"Content-Type":    {"application/json"},
		"User-Agent":      {"ua"},
		"X-Request-Id":    {"r1"},
		"Idempotency-Key": {"k1"},
	}
	log.Info("m",
		"Authorization", "Bearer abc",
		"cookie", "c", "set-cookie", "c", "password", "p", "secret", "s", "token", "t",
		"private_key", "pk", "client_secret", "cs", "github_secret", "gs", "refresh_token", "rt",
		"db_password", "dp",
		"innocuous", Secret("sealed-value"),
		"headers", hdr,
		slog.Group("nested", "token", "nt", "fine", "kept"),
		"name", "kept-too",
	)
	out := b.String()
	for _, leak := range []string{
		"Bearer abc", "session=abc", `"p"`, `"s"`, `"t"`, `"pk"`, `"cs"`, `"gs"`,
		`"rt"`, `"dp"`, "sealed-value", `"nt"`, "base-attr-value",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("%s leaked: %s", leak, out)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"Authorization", "cookie", "set-cookie", "password", "secret", "token", "private_key",
		"client_secret", "github_secret", "refresh_token", "db_password", "innocuous", "api_token",
	} {
		if m[k] != Redacted {
			t.Errorf("%s = %v", k, m[k])
		}
	}
	headers, _ := m["headers"].(map[string]any)
	if len(headers) != 4 || headers["Authorization"] != nil || headers["Cookie"] != nil ||
		headers["Idempotency-Key"] == nil {
		t.Errorf("headers: %v", headers)
	}
	nested, _ := m["nested"].(map[string]any)
	if nested["token"] != Redacted || nested["fine"] != "kept" || m["name"] != "kept-too" {
		t.Errorf("groups: %v", m)
	}

	b.Reset()
	slog.New(Redact(slog.NewJSONHandler(&b, nil))).WithGroup("g").Info("m", "token", "t2")
	if strings.Contains(b.String(), "t2") {
		t.Errorf("a group's attribute leaked: %s", b.String())
	}
	s := Secret("x")
	if s.String() != Redacted || s.Reveal() != "x" {
		t.Errorf("Secret: %s %s", s.String(), s.Reveal())
	}
	if Redact(
		slog.NewJSONHandler(&b, &slog.HandlerOptions{Level: slog.LevelWarn}),
	).Enabled(t.Context(), slog.LevelInfo) {
		t.Error("Enabled ignores the wrapped handler")
	}
}

func TestUnitFollowLevel(t *testing.T) {
	var b bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&b, nil))
	lv := new(slog.LevelVar)
	sig := make(chan os.Signal)
	next := make(chan func() (slog.Level, error))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		FollowLevel(ctx, sig, func() (slog.Level, error) { return (<-next)() }, lv, log)
	}()
	sig <- syscall.SIGHUP
	next <- func() (slog.Level, error) { return slog.LevelDebug, nil }
	sig <- syscall.SIGHUP // taken only once the first reload is done
	if lv.Level() != slog.LevelDebug {
		t.Errorf("level %v after a reload to debug", lv.Level())
	}
	next <- func() (slog.Level, error) { return 0, errors.New("bad file") }
	cancel()
	<-done
	if lv.Level() != slog.LevelDebug || !strings.Contains(b.String(), "bad file") ||
		!strings.Contains(b.String(), `"log_level":"debug"`) {
		t.Errorf("level %v, logs %s", lv.Level(), b.String())
	}
}

func TestUnitWarnings(t *testing.T) {
	var b bytes.Buffer
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	w := NewWarnings(slog.New(slog.NewJSONHandler(&b, nil)), c.now)
	ctx := t.Context()
	w.Warn(ctx, "otlp", "export failed", "error", "refused")
	for range 3 {
		c.advance(10 * time.Second)
		w.Warn(ctx, "otlp", "export failed")
	}
	w.Warn(ctx, "webhook", "delivery failed")
	c.advance(31 * time.Second)
	w.Warn(ctx, "otlp", "export failed")
	got := lines(t, &b)
	if len(got) != 3 || got[0]["suppressed"] != 0.0 || got[0]["error"] != "refused" ||
		got[1]["source"] != "webhook" || got[2]["suppressed"] != 3.0 {
		t.Errorf("%v", got)
	}
}
