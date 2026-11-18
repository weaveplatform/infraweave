package gitlabfake_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
)

func TestUnitWebhookSenderOneAttemptAndResend(t *testing.T) {
	secret := []byte("gitlab-fake-secret-at-least-32-bytes")
	delivery := gitlabfake.WebhookDelivery{ID: "gitlab-uuid", Event: "Push Hook", Body: []byte(`{"object_kind":"push"}`)}
	var calls atomic.Int64
	r := chi.NewRouter()
	codes := []int{503, 429, 202}
	r.Post("/receive", func(w http.ResponseWriter, r *http.Request) {
		index := int(calls.Add(1)) - 1
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, delivery.Body) || r.Header.Get("X-Gitlab-Token") != string(secret) || r.Header.Get("X-Gitlab-Event") != delivery.Event ||
			r.Header.Get("X-Gitlab-Event-UUID") != delivery.ID ||
			r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Authorization") != "" {
			t.Error("webhook request lost its documented headers or exact bytes")
		}
		w.WriteHeader(codes[index])
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	sender := gitlabfake.NewWebhookSender(nil)
	for index, code := range codes {
		status, err := sender.Send(t.Context(), srv.URL+"/receive", secret, delivery)
		if err != nil || status != code || calls.Load() != int64(index+1) {
			t.Fatalf("explicit attempt %d: status=%d requests=%d err=%v", index, status, calls.Load(), err)
		}
	}
	attempts := sender.Attempts()
	if len(attempts) != len(codes) {
		t.Fatal("attempt history missing")
	}
	for index, attempt := range attempts {
		if attempt.Status != codes[index] || attempt.ID != delivery.ID || attempt.Event != delivery.Event {
			t.Fatal("attempt history changed")
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", attempts), string(secret)) {
		t.Fatal("attempt history retained the token")
	}
	attempts[0].Status = 999
	if sender.Attempts()[0].Status != 503 {
		t.Fatal("attempt history was not copied")
	}
}

type webhookTransport func(*http.Request) (*http.Response, error)

func (f webhookTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failedWebhookBody struct{}

func (failedWebhookBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failedWebhookBody) Close() error             { return nil }

func TestUnitWebhookSenderFailuresAndRedirects(t *testing.T) {
	delivery := gitlabfake.WebhookDelivery{Event: "Note Hook", Body: []byte(`{}`)}
	secret := []byte("gitlab-fake-secret-at-least-32-bytes")
	for _, tc := range []struct {
		name, url string
		transport http.RoundTripper
		status    int
		cause     error
	}{
		{name: "bad URL", url: ":bad"},
		{name: "transport refusal", url: "http://example.com", transport: webhookTransport(func(*http.Request) (*http.Response, error) { return nil, io.ErrClosedPipe }), cause: io.ErrClosedPipe},
		{name: "body refusal", url: "http://example.com", transport: webhookTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 202, Body: failedWebhookBody{}, Header: http.Header{}}, nil
		}), status: 202, cause: io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := gitlabfake.NewWebhookSender(&http.Client{Transport: tc.transport})
			status, err := sender.Send(t.Context(), tc.url, secret, delivery)
			if err == nil || tc.cause != nil && !errors.Is(err, tc.cause) || status != tc.status || len(sender.Attempts()) != 1 || sender.Attempts()[0].Status != tc.status {
				t.Fatal("failure did not produce exactly one safe attempt", status, err)
			}
		})
	}
	var sink, redirects int
	r := chi.NewRouter()
	r.Post("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/sink", http.StatusTemporaryRedirect)
	})
	r.Post("/sink", func(http.ResponseWriter, *http.Request) { sink++ })
	srv := httptest.NewServer(r)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { redirects++; return nil }}
	sender := gitlabfake.NewWebhookSender(client)
	status, err := sender.Send(t.Context(), srv.URL+"/redirect", secret, delivery)
	if err != nil || status != 307 || sink != 0 || redirects != 0 || client.CheckRedirect == nil {
		t.Fatal("sender followed a redirect or changed the caller's client", err)
	}
	if err := client.CheckRedirect(nil, nil); err != nil || redirects != 1 {
		t.Fatal("caller redirect policy changed")
	}
}

func TestUnitWebhookSenderConcurrentHistory(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(202) })
	srv := httptest.NewServer(r)
	defer srv.Close()
	sender := gitlabfake.NewWebhookSender(srv.Client())
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			status, err := sender.Send(t.Context(), srv.URL, []byte("gitlab-fake-secret-at-least-32-bytes"), gitlabfake.WebhookDelivery{Event: "Push Hook"})
			if err != nil || status != 202 {
				t.Error("send failed", err)
			}
		})
	}
	workers.Wait()
	attempts := sender.Attempts()
	if len(attempts) != 20 || slices.ContainsFunc(attempts, func(a gitlabfake.WebhookAttempt) bool { return a.Status != 202 }) {
		t.Fatal("concurrent attempts lost")
	}
}
