package bitbucketcloud_test

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCommitCheckRateLimitsReachQueueBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		status       int
		reset        bool
	}{
		{"lookup limit", http.MethodGet, 403, true},
		{"write limit", http.MethodPost, 403, true},
		{"too many requests", http.MethodPost, 429, true},
		{"permission refusal", http.MethodPost, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLab(t)
			reset := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
			hits := 0
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == tc.method && strings.Contains(r.URL.Path, "statuses") {
					hits++
					w.Header().Set("Content-Type", "application/json")
					if tc.reset {
						w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"message":"denied","error":{"message":"denied"}}`))
					return
				}
				l.fake.Handler().ServeHTTP(w, r)
			})
			p := bitbucketcloud.NewOver(memory{h}, l.srv.URL)
			_, err := p.Set(
				t.Context(),
				l.repo.Repo,
				l.repo.Head,
				port.CheckReport{Name: "infraweave/infra", State: port.CheckQueued, Title: "Queued", URL: "https://infra.example/runs/one", RecoveryID: "infraweave/run:one"},
			)
			limited, ok := errors.AsType[*port.RateLimited](err)
			if err == nil || hits != 1 || ok != tc.reset || ok && !limited.Reset.Equal(reset) {
				t.Fatal("native refusal was retried, hidden, or misclassified", err, hits, limited)
			}
		})
	}
}

func TestUnitCommitCheckLookupAndWriteHaveBoundedDeadlines(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			l := newLab(t)
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == method && strings.Contains(r.URL.Path, "statuses") {
					<-r.Context().Done()
					return
				}
				l.fake.Handler().ServeHTTP(w, r)
			})
			p := bitbucketcloud.NewOver(memory{h}, l.srv.URL)
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				_, err := p.Set(
					t.Context(),
					l.repo.Repo,
					l.repo.Head,
					port.CheckReport{Name: "infraweave/infra", State: port.CheckQueued, Title: "Queued", URL: "https://infra.example/runs/one", RecoveryID: "infraweave/run:one"},
				)
				if !errors.Is(err, kernel.ErrUnavailable) || time.Since(start) != bitbucketcloud.Timeout {
					t.Fatal("native check did not honor its call deadline", err, time.Since(start))
				}
			})
		})
	}
}
