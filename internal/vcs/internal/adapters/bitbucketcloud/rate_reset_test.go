package bitbucketcloud_test

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCommentRateResetHeaders(t *testing.T) {
	reset := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Second)
	for _, tc := range []struct {
		label, header, value string
		status               int
		limited, sdk         bool
	}{
		{"epoch", "X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10), 403, true, false},
		{"alternate epoch", "RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10), 403, true, false},
		{"seconds", "Retry-After", "120", 403, true, false},
		{"date", "Retry-After", reset.Format(http.TimeFormat), 403, true, false},
		{"429 date", "Retry-After", reset.Format(http.TimeFormat), 429, true, false},
		{"two headers", "X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10), 403, true, false},
		{"invalid primary valid fallback", "X-RateLimit-Reset", "invalid", 403, true, false},
		{"permission", "", "", 403, false, false},
		{"negative seconds", "Retry-After", "-1", 403, false, false},
		{"negative epoch", "X-RateLimit-Reset", "-1", 403, false, false},
		{"negative alternate epoch", "RateLimit-Reset", "-1", 403, false, false},
		{"malformed seconds", "Retry-After", "invalid", 403, false, false},
		{"overflow seconds", "Retry-After", "9223372037", 403, false, false},
		{"maximum integer seconds", "Retry-After", "9223372036854775807", 403, false, false},
		{"secondary negative", "Retry-After", "-1", 403, false, true},
		{"secondary overflow", "Retry-After", "9223372037", 403, false, true},
		{"fallback", "", "", 429, true, false},
		{"invalid fallback", "Retry-After", "-1", 429, true, false},
		{"overflow fallback", "Retry-After", "9223372037", 429, true, false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			l := newLab(t)
			calls := 0
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/comments") {
					calls++
					w.Header().Set("Content-Type", "application/json")
					if tc.header != "" {
						w.Header().Set(tc.header, tc.value)
					}
					if tc.header == "X-RateLimit-Reset" {
						w.Header().Set("X-RateLimit-Remaining", "0")
					}
					if tc.label == "two headers" {
						if tc.header == "Retry-After" {
							w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Add(time.Hour).Unix(), 10))
						} else {
							w.Header().Set("Retry-After", "3600")
						}
					}
					if tc.label == "invalid primary valid fallback" {
						if tc.header == "Retry-After" {
							w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
						} else {
							w.Header().Set("Retry-After", reset.Format(http.TimeFormat))
						}
					}
					w.WriteHeader(tc.status)
					body := `{"message":"denied","error":{"message":"denied"},"errors":[{"message":"denied"}]}`
					if tc.sdk {
						body = `{"message":"denied","documentation_url":"https://docs.github.com/rest/using-the-rest-api/rate-limits-for-the-rest-api#about-secondary-rate-limits"}`
					}
					_, _ = w.Write([]byte(body))
					return
				}
				l.fake.Handler().ServeHTTP(w, r)
			})
			provider := bitbucketcloud.NewOver(memory{handler}, "https://bitbucket.example/2.0")
			before := time.Now()
			_, err := provider.Post(t.Context(), l.repo.Repo, commentRequest("plan:rate", "body"))
			limited, ok := errors.AsType[*port.RateLimited](err)
			if err == nil || calls != 1 || ok != tc.limited {
				t.Fatal("native header refusal misclassified or retried", err, calls, limited)
			}
			if tc.status == 403 && !tc.limited && !errors.Is(err, port.ErrCredential) {
				t.Fatal("invalid reset hid a permission refusal", err)
			}
			if ok {
				switch {
				case tc.status == 429 && strings.Contains(tc.label, "fallback"):
					if limited.Reset.Before(before.Add(59*time.Second)) || limited.Reset.After(time.Now().Add(time.Minute)) {
						t.Fatal("invalid or absent reset lost fallback backoff", limited)
					}
				case tc.header == "Retry-After" && tc.value == "120":
					if limited.Reset.Before(before.Add(119*time.Second)) || limited.Reset.After(time.Now().Add(120*time.Second)) {
						t.Fatal("relative reset overflowed", limited)
					}
				default:
					if !limited.Reset.Equal(reset) {
						t.Fatal("explicit reset changed", limited, reset)
					}
				}
			}
		})
	}
}
