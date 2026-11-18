package webhooktest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Provider uses GitHub's safe metadata names while keeping its payload minimal.
type Provider struct {
	Calls      *[]string
	Parsed     *int
	ParseError error
}

var _ port.Webhooks = Provider{}

var errSignature = errors.New("test webhook signature refused")

// Sign is the hex HMAC-SHA256 used both by verification and retained replay data.
func Sign(body, secret []byte) []byte {
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write(body)
	return []byte(hex.EncodeToString(m.Sum(nil)))
}

func (p Provider) called(name string) {
	if p.Calls != nil {
		*p.Calls = append(*p.Calls, name)
	}
}

// Verify compares every supplied secret in constant time.
func (p Provider) Verify(h http.Header, b []byte, secrets [][]byte) ([]byte, error) {
	p.called("verify")
	got := []byte(h.Get("X-Hub-Signature-256"))
	var matched []byte
	for _, secret := range secrets {
		sig := Sign(b, secret)
		if hmac.Equal(got, append([]byte("sha256="), sig...)) {
			matched = sig
		}
	}
	if matched == nil {
		return nil, errSignature
	}
	return matched, nil
}

// DeliveryID is the provider identity; no header exercises intake's body fallback.
func (p Provider) DeliveryID(h http.Header, _ []byte) string {
	p.called("id")
	return h.Get("X-GitHub-Delivery")
}

// Event is the safe event header.
func (p Provider) Event(h http.Header, _ []byte) string {
	p.called("event")
	return h.Get("X-GitHub-Event")
}

// Consumed consumes pushes and ignores pings after verification.
func (p Provider) Consumed(event string) bool { p.called("filter"); return event == "push" }

// Repository is a stable provider ID, independent of the payload's path.
func (p Provider) Repository(_ []byte) string { p.called("repository"); return "repository-7" }

// Parse normalizes the fixture event and can expose a synthetic parser failure.
func (p Provider) Parse(_ http.Header, b []byte) ([]vcsapi.Event, error) {
	p.called("parse")
	if p.Parsed != nil {
		*p.Parsed++
	}
	if p.ParseError != nil {
		return nil, p.ParseError
	}
	var e vcsapi.Event
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("test webhook: parse event: %w", err)
	}
	return []vcsapi.Event{e}, nil
}
