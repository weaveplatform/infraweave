package deliveryfake

import (
	"bytes"
	"context"
	"crypto/fips140"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

// ErrDelivery refuses invalid metadata, redelivery indexes or signing keys
// that the active cryptographic policy does not permit.
var ErrDelivery = errors.New("webhook fake: invalid delivery")

// Config is the two providers' documented header and retry policy.
type Config struct {
	DeliveryHeader string
	AttemptHeader  string
	HookHeader     string
	Attempts       int
}

// Attempt is one actual HTTP attempt; status zero is a transport failure.
type Attempt struct {
	DeliveryID string
	Event      string
	Number     int
	Status     int
}

type request struct {
	id, event, target string
	body              []byte
}

// Sender retains immutable requests for explicit redelivery.
type Sender struct {
	mu       sync.Mutex
	secret   []byte
	client   *http.Client
	config   Config
	hookID   string
	requests []request
	attempts []Attempt
}

// New clones client, preserving its transport and timeout. A nil client gets
// a ten-second fixture bound. Redirects always remain separate responses.
func New(secret []byte, client *http.Client, config Config) *Sender {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	config.Attempts = max(1, config.Attempts)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	return &Sender{
		secret: slices.Clone(secret), client: &copy, config: config,
		hookID: fmt.Sprintf("{%x-%x-%x-%x-%x}", id[:4], id[4:6], id[6:8], id[8:10], id[10:]),
	}
}

// Send posts a logical delivery, retrying only 5xx where the provider permits.
func (s *Sender) Send(ctx context.Context, target, id, event string, body []byte) (int, error) {
	if id == "" || event == "" {
		return 0, ErrDelivery
	}
	return s.send(ctx, request{id: id, event: event, target: target, body: slices.Clone(body)})
}

// Redeliver sends the exact request from completed attempt index again.
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

// Attempts returns an independent snapshot of all completed attempts.
func (s *Sender) Attempts() []Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.attempts)
}

func (s *Sender) send(ctx context.Context, d request) (int, error) {
	if len(s.secret) > 0 && fips140.Enforced() && len(s.secret) < 14 {
		return 0, ErrDelivery
	}
	var status int
	for number := 1; number <= s.config.Attempts; number++ {
		var err error
		status, err = s.attempt(ctx, d, number)
		if err != nil || status < 500 || status > 599 {
			return status, err
		}
	}
	return status, nil
}

func (s *Sender) attempt(ctx context.Context, d request, number int) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.target, bytes.NewReader(d.body))
	if err != nil {
		return 0, fmt.Errorf("webhook fake: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Bitbucket-Webhooks/fake")
	req.Header.Set("X-Event-Key", d.event)
	req.Header.Set(s.config.DeliveryHeader, d.id)
	if s.config.AttemptHeader != "" {
		req.Header.Set(s.config.AttemptHeader, strconv.Itoa(number))
	}
	if s.config.HookHeader != "" {
		req.Header.Set(s.config.HookHeader, s.hookID)
	}
	if len(s.secret) > 0 {
		m := hmac.New(sha256.New, s.secret)
		_, _ = m.Write(d.body)
		req.Header.Set("X-Hub-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	status := 0
	if resp != nil {
		status = resp.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}
	s.mu.Lock()
	s.requests = append(s.requests, d)
	s.attempts = append(s.attempts, Attempt{DeliveryID: d.id, Event: d.event, Number: number, Status: status})
	s.mu.Unlock()
	if err != nil {
		return status, fmt.Errorf("webhook fake: send: %w", err)
	}
	return status, nil
}
