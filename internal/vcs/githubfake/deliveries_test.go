package githubfake_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
)

func TestUnitDeliverySender(t *testing.T) {
	f := githubfake.New()
	app := f.AddApp("infraweave", "acme")
	sender, err := f.Sender(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := githubfake.Payload("push", "")
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), body...)
	const id = "72d3162e-7c72-11e3-81e9-123456789abc"
	var calls atomic.Int64
	r := chi.NewRouter()
	r.Post("/hook", func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		got, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
		}
		m := hmac.New(sha256.New, []byte(app.WebhookSecret))
		_, _ = m.Write(want)
		if string(got) != string(want) || req.Header.Get("X-Hub-Signature-256") != "sha256="+hex.EncodeToString(m.Sum(nil)) ||
			req.Header.Get("X-GitHub-Delivery") != id || req.Header.Get("X-GitHub-Event") != "push" ||
			req.Header.Get("Content-Type") != "application/json" || !strings.HasPrefix(req.Header.Get("User-Agent"), "GitHub-Hookshot/") ||
			req.Header.Get("X-Hub-Signature") != "" {
			t.Error("sender changed body, signing scheme or delivery metadata")
		}
		if calls.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	if status, err := sender.Send(t.Context(), srv.URL+"/hook", id, "push", body); err != nil || status != 503 || calls.Load() != 1 {
		t.Fatalf("one failed attempt = %d, %v, calls=%d", status, err, calls.Load())
	}
	body[0] = 'x'
	if status, err := sender.Redeliver(t.Context(), 0); err != nil || status != 202 || calls.Load() != 2 {
		t.Fatalf("explicit redelivery = %d, %v, calls=%d", status, err, calls.Load())
	}
	attempts := sender.Attempts()
	if diff := cmp.Diff([]githubfake.Attempt{{DeliveryID: id, Event: "push", Status: 503}, {DeliveryID: id, Event: "push", Status: 202}}, attempts); diff != "" {
		t.Fatal(diff)
	}
	attempts[0].Status = 200
	if sender.Attempts()[0].Status != 503 {
		t.Fatal("attempt snapshot aliases sender storage")
	}
}

func TestUnitDeliverySenderRefusals(t *testing.T) {
	f := githubfake.New()
	if _, err := f.Sender(0); !errors.Is(err, githubfake.ErrDelivery) {
		t.Fatalf("unknown App = %v", err)
	}
	app := f.AddApp("infraweave", "acme")
	sender, err := f.Sender(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{-1, 0} {
		if _, err := sender.Redeliver(t.Context(), index); !errors.Is(err, githubfake.ErrDelivery) {
			t.Fatalf("redeliver %d: %v", index, err)
		}
	}
	if _, err := sender.Send(t.Context(), "http://example.com", "", "push", nil); !errors.Is(err, githubfake.ErrDelivery) {
		t.Fatalf("empty ID = %v", err)
	}
	if _, err := sender.Send(t.Context(), "http://example.com", "delivery", "", nil); !errors.Is(err, githubfake.ErrDelivery) {
		t.Fatalf("empty event = %v", err)
	}
	if _, err := sender.Send(t.Context(), "://", "delivery", "push", nil); err == nil {
		t.Fatal("invalid URL succeeded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if status, err := sender.Send(
		ctx,
		"http://127.0.0.1:1",
		"delivery",
		"push",
		nil,
	); err == nil || status != 0 || len(sender.Attempts()) != 1 ||
		sender.Attempts()[0].Status != 0 {
		t.Fatalf("failed transport = %d, %v, attempts=%+v", status, err, sender.Attempts())
	}
	if _, err := githubfake.Payload("unknown", ""); !errors.Is(err, githubfake.ErrEvent) {
		t.Fatalf("unknown payload = %v", err)
	}
}

func TestUnitDeliverySenderDoesNotFollowRedirect(t *testing.T) {
	f := githubfake.New()
	app := f.AddApp("infraweave", "acme")
	sender, err := f.Sender(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	r := chi.NewRouter()
	r.Post("/redirect", func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		http.Redirect(w, req, "/redirect", http.StatusTemporaryRedirect)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	if status, err := sender.Send(t.Context(), srv.URL+"/redirect", "delivery", "push", nil); err != nil || status != 307 || calls.Load() != 1 {
		t.Fatalf("redirect = %d, %v, calls=%d", status, err, calls.Load())
	}
}
