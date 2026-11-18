package ratelimit_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/ratelimit"
)

func TestUnitResetHeaders(t *testing.T) {
	now := time.Unix(2000000000, 0)
	for _, tc := range []struct {
		name, value string
		wait        time.Duration
		valid       bool
	}{
		{"X-RateLimit-Reset", "2000000120", 2 * time.Minute, true},
		{"RateLimit-Reset", "2000000120", 2 * time.Minute, true},
		{"Retry-After", "120", 2 * time.Minute, true},
		{"Retry-After", "9223372036", 9223372036 * time.Second, true},
		{"Retry-After", now.Add(time.Minute).UTC().Format(http.TimeFormat), time.Minute, true},
		{"Retry-After", "-1", 0, false},
		{"Retry-After", "9223372037", 0, false},
		{"Retry-After", "9223372036854775807", 0, false},
		{"X-RateLimit-Reset", "-1", 0, false},
		{"RateLimit-Reset", "-1", 0, false},
		{"X-RateLimit-Reset", "invalid", 0, false},
		{"Retry-After", "", 0, false},
	} {
		h := http.Header{}
		h.Set(tc.name, tc.value)
		reset, ok := ratelimit.Reset(h, now, "X-RateLimit-Reset", "RateLimit-Reset", "Retry-After")
		if ok != tc.valid || ok && !reset.Equal(now.Add(tc.wait)) {
			t.Fatal(tc, reset, ok)
		}
	}
}
