package specref

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// IntrospectionQuery is the standard introspection query with deprecated
// fields and enum values included, so the snapshot records what upstream
// still serves but has marked for removal.
//
//go:embed introspection.graphql
var IntrospectionQuery string

// maxResponse bounds a fetched body. The schema is about 1.4 MiB and the
// tarballs a few MiB; 64 MiB leaves headroom without trusting the server.
const maxResponse = 64 << 20

var ErrHTTPStatus = errors.New("unexpected HTTP status")

// Fetcher downloads upstream sources politely: one identified client,
// bounded retries with backoff, and a size cap on every body.
type Fetcher struct {
	Client    *http.Client
	UserAgent string
	Retries   int
	Backoff   time.Duration
}

// Introspect posts the introspection query to a GraphQL endpoint.
func (f *Fetcher) Introspect(ctx context.Context, endpoint string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"query": IntrospectionQuery})
	if err != nil {
		return nil, fmt.Errorf("encode introspection query: %w", err)
	}
	return f.do(ctx, http.MethodPost, endpoint, body)
}

// Get downloads a URL, typically a codeload tarball.
func (f *Fetcher) Get(ctx context.Context, url string) ([]byte, error) {
	return f.do(ctx, http.MethodGet, url, nil)
}

func (f *Fetcher) do(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= f.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("%s %s: %w", method, url, ctx.Err())
			case <-time.After(f.Backoff * time.Duration(attempt)):
			}
		}
		out, retry, err := f.once(ctx, method, url, body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return nil, lastErr
}

// once makes one request; retry reports whether the failure is transient
// (network error, 429 or 5xx) rather than a fault a retry cannot fix.
func (f *Fetcher) once(ctx context.Context, method, url string, body []byte) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", f.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, true, fmt.Errorf("read %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		transient := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, transient, fmt.Errorf(
			"%w: %s %s: %d",
			ErrHTTPStatus,
			method,
			url,
			resp.StatusCode,
		)
	}
	return data, false, nil
}
