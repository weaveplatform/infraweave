package sdk

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/weaveplatform/infraweave/pkg/client/api"
)

// APIPrefix is the path every served operation sits under (decision 0017),
// the server URL of api/api.openapi.yaml.
const APIPrefix = "/api/v1"

// IdempotencyKeyHeader names the key every POST and PATCH carries
// (decision 0003).
const IdempotencyKeyHeader = "Idempotency-Key"

// Doer sends one HTTP request; *http.Client is one.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client calls an infraweave server with the API's conventions: a bearer
// token, an Idempotency-Key on writes, and retries on 429 and on 503 with
// Retry-After. The generated client in API and the raw Do share them.
type Client struct {
	// API is the generated client of every served operation.
	API *api.ClientWithResponses

	base  string
	doer  Doer
	token string
	key   *keyToken
	retry Retry
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sends through d instead of http.DefaultClient.
func WithHTTPClient(d Doer) Option { return func(c *Client) { c.doer = d } }

// WithToken sends token as the bearer credential, as is, such as a token
// from INFRAWEAVE_API_TOKEN; WithAPIKey exchanges a key for tokens
// instead.
func WithToken(token string) Option { return func(c *Client) { c.token = token } }

// WithRetry replaces DefaultRetry.
func WithRetry(r Retry) Option { return func(c *Client) { c.retry = r } }

// New returns a client of the server at baseURL, its scheme and host with
// no path, such as https://infraweave.example.com.
func New(baseURL string, opts ...Option) (*Client, error) {
	c := &Client{
		base:  strings.TrimSuffix(baseURL, "/"),
		doer:  http.DefaultClient,
		retry: DefaultRetry,
	}
	for _, o := range opts {
		o(c)
	}
	var err error
	c.API, err = api.NewClientWithResponses(c.base+APIPrefix, api.WithHTTPClient(transport{c}))
	if err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}
	return c, nil
}

// transport applies the client's conventions to every request the
// generated client sends.
type transport struct{ c *Client }

func (t transport) Do(req *http.Request) (*http.Response, error) { return t.c.send(req) }

// Response is an answer read in full.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Header sets one request header of a Do call.
type Header struct{ Name, Value string }

// Do sends method to path, which starts at the server's root (/api/v1/…,
// or /livez on the admin listener), with body encoded as JSON when it is
// not nil and with headers set. It returns the answer and, for a status of
// 400 or more, the typed error of its problem as well. A caller's
// Idempotency-Key header replaces the one the SDK would choose.
func (c *Client) Do(
	ctx context.Context,
	method, path string,
	body any,
	headers ...Header,
) (*Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("sdk: %s %s: %w", method, path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, h := range headers {
		req.Header.Set(h.Name, h.Value)
	}
	resp, err := c.send(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sdk: %s %s: %w", method, path, err)
	}
	out := &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}
	return out, Check(resp, b)
}

// send sends req with the token, or the API key's, and, on a write, an
// Idempotency-Key the caller did not set, retrying as c.retry allows. The key is chosen once,
// so every attempt of the call carries the same one and the server
// replays rather than repeats a write that already happened.
func (c *Client) send(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") == "" {
		token := c.token
		if c.key != nil {
			var err error
			if token, err = c.key.bearer(req.Context(), c); err != nil {
				return nil, err
			}
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if (req.Method == http.MethodPost || req.Method == http.MethodPatch) &&
		req.Header.Get(IdempotencyKeyHeader) == "" {
		req.Header.Set(IdempotencyKeyHeader, rand.Text())
	}
	if req.Body != nil && req.GetBody == nil {
		// A retry sends the body again, so it is read once here.
		b, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("sdk: read request body: %w", err)
		}
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
		req.Body, _ = req.GetBody()
	}
	return c.retry.do(req, c.doer)
}
