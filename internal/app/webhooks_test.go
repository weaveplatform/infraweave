package app

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
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	vcsassembly "github.com/weaveplatform/infraweave/internal/vcs/assembly"
)

type webhookBuckets struct {
	keys []ratelimit.Key
	err  error
}

func (b *webhookBuckets) Take(_ context.Context, k ratelimit.Key, _ ratelimit.Limit) (ratelimit.State, error) {
	b.keys = append(b.keys, k)
	return ratelimit.State{Allowed: true}, b.err
}

type webhookSwitch struct{ err error }

func (s webhookSwitch) Disabled(context.Context, string) (bool, error) { return false, s.err }

type untouchedBody struct{ read bool }

func (b *untouchedBody) Read([]byte) (int, error) {
	b.read = true
	return 0, errors.New("body must not be read")
}
func (*untouchedBody) Close() error { return nil }

func TestUnitWebhookControls(t *testing.T) {
	for _, status := range []int{200, 202, 401, 404, 413, 503, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := config.Defaults().Server
			c.MaxBodyBytes = 1
			buckets := &webhookBuckets{}
			g := memory(c)
			g.Buckets = buckets
			var logs bytes.Buffer
			r := Guarded(slog.New(slog.NewJSONHandler(&logs, nil)), c, apiOperations(), g, byPrefix{api: byAddress{}}, nil, nil)
			r.Post(vcsassembly.WebhookPattern, func(w http.ResponseWriter, _ *http.Request) {
				if status == 500 {
					panic("fixture panic")
				}
				w.Header().Set("WWW-Authenticate", "must-not-escape")
				http.Error(w, "must-not-escape", status)
			})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/vcs/gitlab/id", strings.NewReader("larger-than-api-bound"))
			req.RemoteAddr = "192.0.2.1:1234"
			req.Header.Set("Authorization", "Bearer ignored")
			req.Header.Set("Idempotency-Key", "ignored key")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != status || rec.Body.Len() != 0 || rec.Header().Get("WWW-Authenticate") != "" || rec.Header().Get("Content-Type") != "" ||
				rec.Header().Get("X-Request-Id") == "" ||
				rec.Header().Get("Cache-Control") != "no-store" ||
				rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("protocol answer=%d,%v,%s", rec.Code, rec.Header(), rec.Body)
			}
			if len(buckets.keys) != 1 || buckets.keys[0] != (ratelimit.Key{Caller: "192.0.2.1", Class: ratelimit.Expensive}) {
				t.Fatalf("wrong budget identity: %v", buckets.keys)
			}
			lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
			var access map[string]any
			if json.Unmarshal([]byte(lines[len(lines)-1]), &access) != nil || access["bytes"] != float64(0) || access["status"] != float64(status) {
				t.Fatalf("access log=%s", logs.String())
			}
		})
	}
}

func TestUnitWebhookThrottleAndFaults(t *testing.T) {
	c := config.Defaults().Server
	c.RateLimit.Expensive = ratelimit.Limit{Rate: 1e-9, Burst: 2}
	g := memory(c)
	r := Guarded(discard(), c, apiOperations(), g, byPrefix{api: byAddress{}}, nil, nil)
	calls := 0
	r.Post(vcsassembly.WebhookPattern, func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(401) })
	for i := range 3 {
		body := &untouchedBody{}
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/webhooks/vcs/gitlab/id", nil)
		req.Body = body
		req.RemoteAddr = "192.0.2.4:1234"
		req.Header.Set("X-Hub-Signature-256", "sha256=verified-or-refused")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if body.read || rec.Body.Len() != 0 {
			t.Fatal("throttle read body or emitted JSON")
		}
		if i == 2 && (rec.Code != 429 || rec.Header().Get("Retry-After") == "" || calls != 2) {
			t.Fatalf("throttle=%d %v calls=%d", rec.Code, rec.Header(), calls)
		}
	}
	for _, switchFault := range []bool{false, true} {
		g := memory(c)
		if switchFault {
			g.Switch = webhookSwitch{kernel.ErrUnavailable}
		} else {
			g.Buckets = &webhookBuckets{err: kernel.ErrUnavailable}
		}
		r := Guarded(discard(), c, apiOperations(), g, byPrefix{api: byAddress{}}, nil, nil)
		r.Post(vcsassembly.WebhookPattern, func(http.ResponseWriter, *http.Request) { t.Fatal("failed guard reached handler") })
		rec := request(t, r, "POST", "/webhooks/vcs/gitlab/id")
		if rec.Code != 503 || rec.Body.Len() != 0 || rec.Header().Get("Retry-After") == "" || rec.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("guard fault=%d,%v,%s", rec.Code, rec.Header(), rec.Body)
		}
	}
	// A similar-looking prefix retains the API's normal authentication/problem shape.
	h := API(discard(), c, memory(c), Domains{}, Platform{}, Blobs{})
	rec := request(t, h, "POST", "/webhooks/vcsevil/gitlab/id")
	if rec.Body.Len() == 0 {
		t.Fatal("protocol exemption escaped its path boundary")
	}
}

func TestUnitWebhookWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &webhookWriter{ResponseWriter: rec}
	if n, err := io.WriteString(w, "discard"); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	w.WriteHeader(500)
	if rec.Code != 200 || rec.Body.Len() != 0 || w.Unwrap() != rec {
		t.Fatal("writer changed status or connection")
	}
}
