package gitlabfake

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"
)

// WebhookDelivery is the documented event and body sent with a request UUID.
// It holds no token, so explicit resends need the caller's secret again.
type WebhookDelivery struct {
	ID, Event string
	Body      []byte
}

// WebhookAttempt records only safe delivery identity and the observed status;
// Status zero means that no HTTP answer reached the sender.
type WebhookAttempt struct {
	ID, Event string
	Status    int
}

// WebhookSender makes exactly one attempt per Send. Calling Send again with
// the same delivery models GitLab's explicit "Resend request" operation.
type WebhookSender struct {
	client   http.Client
	mu       sync.Mutex
	attempts []WebhookAttempt
}

// NewWebhookSender retains the client's transport and timeout without changing
// it. Redirects are answers, not permission to resend the secret to another URL.
func NewWebhookSender(client *http.Client) *WebhookSender {
	var owned http.Client
	if client != nil {
		owned = *client
	}
	if owned.Timeout == 0 {
		owned.Timeout = 15 * time.Second
	}
	owned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &WebhookSender{client: owned}
}

// Attempts returns an independent snapshot without token, payload or headers.
func (s *WebhookSender) Attempts() []WebhookAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.attempts)
}

// Send posts a single delivery, including a refused attempt in Attempts. It
// does not schedule retries, including for 429/5xx or transport failures.
func (s *WebhookSender) Send(ctx context.Context, endpoint string, token []byte, delivery WebhookDelivery) (status int, err error) {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.attempts = append(s.attempts, WebhookAttempt{ID: delivery.ID, Event: delivery.Event, Status: status})
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bytes.Clone(delivery.Body)))
	if err != nil {
		return 0, fmt.Errorf("gitlabfake: create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitLab/19.0")
	req.Header.Set("X-Gitlab-Token", string(token))
	req.Header.Set("X-Gitlab-Event", delivery.Event)
	if delivery.ID != "" {
		req.Header.Set("X-Gitlab-Event-UUID", delivery.ID)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("gitlabfake: send webhook: %w", err)
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		return status, fmt.Errorf("gitlabfake: read webhook answer: %w", err)
	}
	return status, nil
}
