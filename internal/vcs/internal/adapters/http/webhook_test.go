package http_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	adapter "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

type receiver struct {
	calls int
	code  int
	err   error
}

func (s *receiver) Receive(_ context.Context, provider, id string, _ http.Header, _ []byte) (int, error) {
	if provider != "gitlab" || id != "integration" {
		panic("wrong route parameters")
	}
	s.calls++
	return s.code, s.err
}

type boundedBody struct{ remaining, read int64 }

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), b.remaining)
	clear(p[:n])
	b.remaining -= n
	b.read += n
	return int(n), nil
}
func (*boundedBody) Close() error { return nil }

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("body failed") }
func (brokenBody) Close() error             { return nil }

func TestUnitWebhookAdapter(t *testing.T) {
	for _, tc := range []struct {
		code int
		err  error
		want int
	}{{202, nil, 202}, {200, nil, 200}, {0, intake.ErrNotFound, 404}, {0, intake.ErrVerification, 401}, {0, errors.New("unavailable"), 503}} {
		s := &receiver{code: tc.code, err: tc.err}
		r := chi.NewRouter()
		r.Method("POST", adapter.WebhookPattern, adapter.Webhook(s))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "POST", "/webhooks/vcs/gitlab/integration", strings.NewReader(`{}`)))
		if rec.Code != tc.want || rec.Body.Len() != 0 || s.calls != 1 {
			t.Fatalf("adapter=%d,%s calls=%d", rec.Code, rec.Body, s.calls)
		}
	}
}

func TestUnitWebhookBoundBeforeLookup(t *testing.T) {
	for _, knownSize := range []bool{false, true} {
		s := &receiver{}
		r := chi.NewRouter()
		r.Method("POST", adapter.WebhookPattern, adapter.Webhook(s))
		body := &boundedBody{remaining: intake.MaxBody + 1}
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/webhooks/vcs/gitlab/integration", nil)
		req.Body = body
		if knownSize {
			req.ContentLength = intake.MaxBody + 1
		} else {
			req.ContentLength = -1
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != 413 || s.calls != 0 || body.read > intake.MaxBody+1 || knownSize && body.read != 0 {
			t.Fatalf("bound: status=%d calls=%d read=%d", rec.Code, s.calls, body.read)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/", nil)
	req.Body = brokenBody{}
	adapter.Webhook(nil).ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	rec = httptest.NewRecorder()
	adapter.Webhook(nil).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "POST", "/", nil))
	if rec.Code != 503 {
		t.Fatal(rec.Code)
	}
}
