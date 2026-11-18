package azuredevopsfake_test

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"testing/synctest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
)

func TestUnitWebhookTransientRetries(t *testing.T) {
	password := []byte("azure-webhook-password-at-least-32-bytes")
	delivery := azuredevopsfake.WebhookDelivery{Body: []byte(`{"id":"stable-id","eventType":"git.push","resource":{}}`)}
	var calls atomic.Int64
	r := chi.NewRouter()
	r.Post("/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		user, secret, ok := r.BasicAuth()
		if err != nil || !bytes.Equal(body, delivery.Body) || !ok || user != "" || secret != string(password) || r.Header.Get("X-Event-Type") != "" {
			t.Error("retry changed exact envelope or empty-username Basic credential")
		}
		w.WriteHeader(503)
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	var waits []time.Duration
	sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: srv.Client(), Wait: func(ctx context.Context, delay time.Duration) error {
		if ctx != t.Context() {
			t.Error("retry lost caller context")
		}
		waits = append(waits, delay)
		return nil
	}})
	status, err := sender.Send(t.Context(), srv.URL, "", password, delivery)
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	if err != nil || status != 503 || calls.Load() != 9 || !slices.Equal(waits, want) {
		t.Fatalf("retry schedule status=%d calls=%d waits=%v err=%v", status, calls.Load(), waits, err)
	}
	attempts := sender.Attempts()
	if len(attempts) != 9 {
		t.Fatal("bounded attempts missing")
	}
	for _, attempt := range attempts {
		if attempt.ID != "stable-id" || attempt.Event != "git.push" || attempt.Status != 503 {
			t.Fatal("retry did not retain event identity and status")
		}
	}
	rendered := fmt.Sprintf("%+v", attempts)
	if strings.Contains(rendered, string(password)) || strings.Contains(rendered, base64.StdEncoding.EncodeToString(append([]byte(":"), password...))) ||
		strings.Contains(rendered, "Authorization") {
		t.Fatal("attempt history retained Basic credentials")
	}
	attempts[0].Status = 999
	if sender.Attempts()[0].Status != 503 {
		t.Fatal("attempt snapshot shared mutable state")
	}
}

type webhookTransport func(*http.Request) (*http.Response, error)

func (f webhookTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func webhookAnswer(code int) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: http.NoBody}
}

func TestUnitWebhookRetryStatusPolicy(t *testing.T) {
	for _, code := range []int{200, 202, 204, 302, 307, 400, 401, 404, 408, 410, 429, 500, 501, 502, 503, 504, 505} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls, waits := 0, 0
			client := &http.Client{Transport: webhookTransport(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return webhookAnswer(code), nil
				}
				return webhookAnswer(202), nil
			})}
			sender := azuredevopsfake.NewWebhookSender(
				azuredevopsfake.WebhookSenderOptions{Client: client, Wait: func(context.Context, time.Duration) error { waits++; return nil }},
			)
			status, err := sender.Send(
				t.Context(),
				"http://example.com",
				"",
				[]byte("password"),
				azuredevopsfake.WebhookDelivery{Body: []byte(`{"id":"one","eventType":"git.push"}`)},
			)
			transient := code == 408 || code == 502 || code == 503 || code == 504
			wantCalls, wantStatus := 1, code
			if transient {
				wantCalls, wantStatus = 2, 202
			}
			if err != nil || status != wantStatus || calls != wantCalls || waits != wantCalls-1 {
				t.Fatal("retry status policy changed", status, calls, waits, err)
			}
		})
	}
}

func TestUnitWebhookDefaultRetryClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var times []time.Time
		client := &http.Client{Transport: webhookTransport(func(*http.Request) (*http.Response, error) {
			times = append(times, time.Now())
			if len(times) == 1 {
				return webhookAnswer(503), nil
			}
			return webhookAnswer(202), nil
		})}
		sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: client})
		status, err := sender.Send(t.Context(), "http://example.com", "", []byte("password"), azuredevopsfake.WebhookDelivery{})
		if err != nil || status != 202 || len(times) != 2 || times[1].Sub(times[0]) != time.Second {
			t.Fatal("default retry clock did not wait exactly its documented delay", status, err)
		}
	})
}

type failedWebhookBody struct{}

func (failedWebhookBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failedWebhookBody) Close() error             { return nil }

type cancelWebhookBody struct{ cancel context.CancelFunc }

func (cancelWebhookBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b cancelWebhookBody) Close() error           { b.cancel(); return nil }

func TestUnitWebhookRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &http.Client{Transport: webhookTransport(func(*http.Request) (*http.Response, error) {
		response := webhookAnswer(503)
		response.Body = cancelWebhookBody{cancel: cancel}
		return response, nil
	})}
	sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: client})
	status, err := sender.Send(ctx, "http://example.com", "", nil, azuredevopsfake.WebhookDelivery{})
	if status != 503 || !errors.Is(err, context.Canceled) || len(sender.Attempts()) != 1 {
		t.Fatal("cancelled retry waited or sent another attempt", status, err)
	}
}

func TestUnitWebhookSenderFailures(t *testing.T) {
	for _, tc := range []struct {
		name, url        string
		client           *http.Client
		wait             func(context.Context, time.Duration) error
		cause            error
		status, attempts int
	}{
		{name: "URL", url: ":bad", attempts: 1},
		{name: "transport", url: "http://example.com", client: &http.Client{Transport: webhookTransport(func(*http.Request) (*http.Response, error) { return nil, io.ErrClosedPipe })}, cause: io.ErrClosedPipe, attempts: 1},
		{name: "response body", url: "http://example.com", client: &http.Client{Transport: webhookTransport(func(*http.Request) (*http.Response, error) {
			response := webhookAnswer(202)
			response.Body = failedWebhookBody{}
			return response, nil
		})}, cause: io.ErrUnexpectedEOF, status: 202, attempts: 1},
		{name: "retry wait", url: "http://example.com", client: &http.Client{Transport: webhookTransport(func(*http.Request) (*http.Response, error) { return webhookAnswer(503), nil })}, wait: func(context.Context, time.Duration) error { return context.Canceled }, cause: context.Canceled, status: 503, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: tc.client, Wait: tc.wait})
			status, err := sender.Send(t.Context(), tc.url, "", []byte("password"), azuredevopsfake.WebhookDelivery{Body: []byte(`not JSON`)})
			if err == nil || tc.cause != nil && !errors.Is(err, tc.cause) || status != tc.status || len(sender.Attempts()) != tc.attempts {
				t.Fatal("failed attempt lost status/cause or retried", status, err)
			}
			if sender.Attempts()[0].ID != "" || sender.Attempts()[0].Event != "" {
				t.Fatal("malformed envelope fabricated identity")
			}
		})
	}
	// A known transient status still retries when its error body is truncated.
	calls := 0
	client := &http.Client{Transport: webhookTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			r := webhookAnswer(503)
			r.Body = failedWebhookBody{}
			return r, nil
		}
		return webhookAnswer(202), nil
	})}
	sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: client, Wait: func(context.Context, time.Duration) error { return nil }})
	if status, err := sender.Send(t.Context(), "http://example.com", "", nil, azuredevopsfake.WebhookDelivery{}); err != nil || status != 202 || calls != 2 {
		t.Fatal("transient response body failure suppressed retry", status, err)
	}
}

func TestUnitWebhookSenderRedirectAndConcurrentHistory(t *testing.T) {
	var sink atomic.Int64
	r := chi.NewRouter()
	r.Post("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/sink", http.StatusTemporaryRedirect)
	})
	r.Post("/sink", func(w http.ResponseWriter, _ *http.Request) { sink.Add(1); w.WriteHeader(202) })
	srv := httptest.NewServer(r)
	defer srv.Close()
	sender := azuredevopsfake.NewWebhookSender(azuredevopsfake.WebhookSenderOptions{Client: srv.Client()})
	if status, err := sender.Send(t.Context(), srv.URL+"/redirect", "", nil, azuredevopsfake.WebhookDelivery{}); err != nil || status != 307 || sink.Load() != 0 {
		t.Fatal("sender followed credential-bearing redirect", status, err)
	}
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			if status, err := sender.Send(t.Context(), srv.URL+"/sink", "", nil, azuredevopsfake.WebhookDelivery{}); err != nil || status != 202 {
				t.Error("concurrent send failed", err)
			}
		})
	}
	workers.Wait()
	if len(sender.Attempts()) != 21 || sink.Load() != 20 {
		t.Fatal("concurrent attempts lost")
	}
}
