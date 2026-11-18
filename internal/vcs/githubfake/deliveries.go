package githubfake

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"
)

// ErrDelivery is an unknown App, an invalid redelivery index or an empty ID.
var ErrDelivery = errors.New("githubfake: invalid delivery")

// Attempt records one HTTP attempt, including a failed transport as status zero.
type Attempt struct {
	DeliveryID string
	Event      string
	Status     int
}

type delivery struct {
	id, event, url string
	body           []byte
}

// Sender sends signed deliveries for one of the fake's Apps. It never retries
// or follows redirects; only Redeliver sends a previously attempted delivery.
type Sender struct {
	mu       sync.Mutex
	secret   []byte
	client   *http.Client
	requests []delivery
	attempts []Attempt
}

// Sender constructs a sender for an App the fake holds, using its webhook
// secret. It does not call GitHub or change the App's installation state.
func (f *Fake) Sender(appID int64) (*Sender, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	app, ok := f.apps[appID]
	if !ok {
		return nil, ErrDelivery
	}
	return &Sender{
		secret: []byte(app.WebhookSecret),
		client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

// Send posts body once, with GitHub's SHA-256 signature, event and delivery
// headers. A non-2xx answer is still a completed attempt, never an automatic retry.
func (s *Sender) Send(ctx context.Context, target, id, event string, body []byte) (int, error) {
	if id == "" || event == "" {
		return 0, ErrDelivery
	}
	return s.send(ctx, delivery{id: id, event: event, url: target, body: slices.Clone(body)})
}

// Redeliver posts the exact bytes, event and delivery ID of attempt index again.
func (s *Sender) Redeliver(ctx context.Context, index int) (int, error) {
	s.mu.Lock()
	if index < 0 || index >= len(s.requests) {
		s.mu.Unlock()
		return 0, ErrDelivery
	}
	d := s.requests[index]
	s.mu.Unlock()
	return s.send(ctx, d)
}

// Attempts is an independent snapshot of every completed attempt's status.
func (s *Sender) Attempts() []Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.attempts)
}

func (s *Sender) send(ctx context.Context, d delivery) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(d.body))
	if err != nil {
		return 0, fmt.Errorf("githubfake: delivery request: %w", err)
	}
	m := hmac.New(sha256.New, s.secret)
	_, _ = m.Write(d.body)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(m.Sum(nil)))
	req.Header.Set("X-GitHub-Delivery", d.id)
	req.Header.Set("X-GitHub-Event", d.event)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitHub-Hookshot/githubfake")
	resp, err := s.client.Do(req)
	status := 0
	if resp != nil {
		status = resp.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}
	s.mu.Lock()
	s.requests = append(s.requests, d)
	s.attempts = append(s.attempts, Attempt{DeliveryID: d.id, Event: d.event, Status: status})
	s.mu.Unlock()
	if err != nil {
		return status, fmt.Errorf("githubfake: send delivery: %w", err)
	}
	return status, nil
}
