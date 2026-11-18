package ratelimit

import (
	"net/http"
	"strconv"
	"time"
)

// Reset returns the first valid native reset in the adapter's header order.
// Retry-After supports bounded delta-seconds and HTTP dates; the two reset
// headers contain Unix seconds. Invalid hints cannot turn a permission refusal
// into a throttle or overflow its retry delay.
func Reset(h http.Header, now time.Time, headers ...string) (time.Time, bool) {
	for _, name := range headers {
		value := h.Get(name)
		switch name {
		case "X-RateLimit-Reset", "RateLimit-Reset":
			if epoch, err := strconv.ParseUint(value, 10, 63); err == nil {
				return time.Unix(int64(epoch), 0), true
			}
		case "Retry-After":
			if seconds, err := strconv.ParseUint(value, 10, 64); err == nil && seconds <= uint64((1<<63-1)/int64(time.Second)) {
				return now.Add(time.Duration(seconds) * time.Second).Truncate(time.Second), true
			}
			if reset, err := http.ParseTime(value); err == nil {
				return reset, true
			}
		}
	}
	return time.Time{}, false
}
