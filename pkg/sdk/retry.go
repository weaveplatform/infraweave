package sdk

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Retry bounds how a call is retried.
//
// A 429, and a 503 with Retry-After, are retried after the wait the server
// names. A call that carries an Idempotency-Key is also retried after a
// 409 (another attempt of it is still in flight) and after a 500, 502, 503
// or 504, waiting Backoff and then twice as long each time: the key makes
// the server replay a write that already happened rather than repeat it.
// Anything else is returned at once.
type Retry struct {
	// Attempts bounds the tries of one call, the first included; 1 never
	// retries.
	Attempts int
	// MaxWait caps one wait. An answer that asks for longer is returned
	// rather than waited for.
	MaxWait time.Duration
	// Backoff is the first wait before retrying an answer that names none.
	Backoff time.Duration
}

// DefaultRetry is the policy a Client starts with.
var DefaultRetry = Retry{Attempts: 5, MaxWait: time.Minute, Backoff: 200 * time.Millisecond}

// NoRetry sends each call once.
var NoRetry = Retry{Attempts: 1}

func (r Retry) do(req *http.Request, doer Doer) (*http.Response, error) {
	keyed := req.Header.Get(IdempotencyKeyHeader) != ""
	backoff := r.Backoff
	for attempt := 1; ; attempt++ {
		resp, err := doer.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sdk: %s %s: %w", req.Method, req.URL.Path, err)
		}
		if attempt >= r.Attempts {
			return resp, nil
		}
		wait, own, ok := r.wait(resp, keyed, backoff)
		if !ok {
			return resp, nil
		}
		if own {
			backoff *= 2
		}
		// The connection is reused only once the body is drained.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		t := time.NewTimer(wait)
		select {
		case <-req.Context().Done():
			t.Stop()
			return nil, fmt.Errorf("sdk: %s %s: %w", req.Method, req.URL.Path, req.Context().Err())
		case <-t.C:
		}
		if req.GetBody != nil {
			if req.Body, err = req.GetBody(); err != nil {
				return nil, fmt.Errorf("sdk: %s %s: %w", req.Method, req.URL.Path, err)
			}
		}
	}
}

// wait reports how long to wait before retrying resp, whether that is the
// policy's own backoff rather than a wait the server named, and whether to
// retry at all. Only the policy's own wait doubles for the next attempt.
func (r Retry) wait(
	resp *http.Response,
	keyed bool,
	backoff time.Duration,
) (wait time.Duration, own, ok bool) {
	after, named := retryAfter(resp.Header.Get("Retry-After"))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests && named,
		resp.StatusCode == http.StatusServiceUnavailable && named:
		wait = after
	case !keyed:
		return 0, false, false
	case resp.StatusCode == http.StatusConflict,
		resp.StatusCode == http.StatusInternalServerError,
		resp.StatusCode == http.StatusBadGateway,
		resp.StatusCode == http.StatusServiceUnavailable,
		resp.StatusCode == http.StatusGatewayTimeout:
		wait, own = backoff, true
		if named {
			wait, own = after, false
		}
	default:
		return 0, false, false
	}
	return wait, own, wait <= r.MaxWait
}

// retryAfter parses Retry-After in either of its forms, delay-seconds or
// an HTTP date (RFC 9110 section 10.2.3).
func retryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0), true
	}
	return 0, false
}
