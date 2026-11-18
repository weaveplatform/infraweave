package azuredevopsfake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"
)

// WebhookDelivery carries the complete Azure envelope. Its body owns the event
// ID and type; no request header or redundant field supplies either identity.
type WebhookDelivery struct{ Body []byte }

// WebhookAttempt keeps only safe body identity and status; zero is no response.
type WebhookAttempt struct {
	ID, Event string
	Status    int
}

// WebhookSenderOptions supplies transport and a deterministic retry clock.
// A nil Wait uses a context-bound timer; neither option changes retry policy.
type WebhookSenderOptions struct {
	Client *http.Client
	Wait   func(context.Context, time.Duration) error
}

// WebhookSender models the documented transient retry schedule. Enduring
// failures and 410 stop here; subscription probation/disablement is outside the
// sender. Attempts never retain credentials, headers or payload bytes.
type WebhookSender struct {
	client   http.Client
	wait     func(context.Context, time.Duration) error
	mu       sync.Mutex
	attempts []WebhookAttempt
}

// NewWebhookSender copies the client configuration and applies the documented
// bounded retry policy, with redirects disabled to protect Basic credentials.
func NewWebhookSender(options WebhookSenderOptions) *WebhookSender {
	var client http.Client
	if options.Client != nil {
		client = *options.Client
	}
	if client.Timeout == 0 {
		client.Timeout = 15 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	wait := options.Wait
	if wait == nil {
		wait = webhookWait
	}
	return &WebhookSender{client: client, wait: wait}
}

func webhookWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("azuredevopsfake: webhook wait: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// Attempts is an independent snapshot containing no request credentials.
func (s *WebhookSender) Attempts() []WebhookAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.attempts)
}

// Send preserves body, event ID and credentials over at most eight retries
// after 408/502/503/504. Delays are 1,2,4,8,16,32,60,60 seconds, as documented at
// https://learn.microsoft.com/en-us/azure/devops/service-hooks/troubleshoot .
// Another explicit Send represents a separate operator resend.
func (s *WebhookSender) Send(ctx context.Context, endpoint, username string, password []byte, delivery WebhookDelivery) (int, error) {
	body, secret := bytes.Clone(delivery.Body), bytes.Clone(password)
	var identity struct {
		ID    string `json:"id"`
		Event string `json:"eventType"`
	}
	if json.Unmarshal(body, &identity) != nil {
		identity.ID, identity.Event = "", ""
	}
	for attempt := 0; ; attempt++ {
		status, err := s.sendOne(ctx, endpoint, username, secret, body, WebhookAttempt{ID: identity.ID, Event: identity.Event})
		if !transientWebhookStatus(status) || attempt == 8 {
			return status, err
		}
		delay := time.Duration(min(1<<attempt, 60)) * time.Second
		if err := s.wait(ctx, delay); err != nil {
			return status, fmt.Errorf("azuredevopsfake: webhook retry wait: %w", err)
		}
	}
}

func transientWebhookStatus(status int) bool {
	return status == 408 || status == 502 || status == 503 || status == 504
}

func (s *WebhookSender) sendOne(ctx context.Context, endpoint, username string, password, body []byte, attempt WebhookAttempt) (status int, err error) {
	defer func() { attempt.Status = status; s.mu.Lock(); s.attempts = append(s.attempts, attempt); s.mu.Unlock() }()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("azuredevopsfake: create webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "AzureDevOps/ServiceHooks")
	request.SetBasicAuth(username, string(password))
	response, err := s.client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("azuredevopsfake: send webhook: %w", err)
	}
	defer response.Body.Close()
	status = response.StatusCode
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return status, fmt.Errorf("azuredevopsfake: read webhook answer: %w", err)
	}
	return status, nil
}
