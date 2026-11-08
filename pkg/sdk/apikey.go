package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ExchangePath is where an API key exchanges for a token.
//
// ref: decision 0006
const ExchangePath = APIPrefix + "/auth/token"

// RefreshBefore is how long before a token's validUntil the client
// exchanges its API key again, so a request never leaves with a token
// that expires on the way.
const RefreshBefore = time.Minute

// ErrNoToken is an exchange answered 2xx without a token, which a server
// that is not infraweave, or a proxy in front of it, can answer.
var ErrNoToken = errors.New("the exchange answered no token")

// WithAPIKey exchanges the API key id and its secret, an iwk_ secret or an
// OIDC key's JWT, at POST /auth/token for a token the client sends with
// every request, and exchanges again once the token is within
// RefreshBefore of its validUntil. The first request exchanges.
func WithAPIKey(id, secret string) Option {
	return func(c *Client) { c.key = &keyToken{id: id, secret: secret} }
}

// keyToken is an API key's current token. mu serializes exchanges, so
// concurrent requests near expiry exchange once.
type keyToken struct {
	id, secret string

	mu    sync.Mutex
	token string
	until time.Time
}

// bearer is a token of k valid beyond RefreshBefore, exchanging for one
// through c when the current one is not.
func (k *keyToken) bearer(ctx context.Context, c *Client) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.token != "" && time.Until(k.until) > RefreshBefore {
		return k.token, nil
	}
	body, err := json.Marshal(map[string]string{"id": k.id, "secret": k.secret})
	if err != nil {
		return "", fmt.Errorf("sdk: exchange API key %s: %w", k.id, err)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.base+ExchangePath,
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("sdk: exchange API key %s: %w", k.id, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	resp, err := c.retry.do(req, c.doer)
	if err != nil {
		return "", fmt.Errorf("sdk: exchange API key %s: %w", k.id, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("sdk: exchange API key %s: %w", k.id, err)
	}
	if err := Check(resp, b); err != nil {
		return "", err
	}
	var u struct {
		JWT        string `json:"jwt"`
		ValidUntil int64  `json:"validUntil"`
	}
	if err := json.Unmarshal(b, &u); err != nil || u.JWT == "" {
		return "", fmt.Errorf("sdk: exchange API key %s: %w", k.id, ErrNoToken)
	}
	k.token, k.until = u.JWT, time.Unix(u.ValidUntil, 0)
	return k.token, nil
}
