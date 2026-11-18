package github_test

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCachedRateResetDuringPagination(t *testing.T) {
	l := newLab(t)
	reset := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	calls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/app/installations") {
			calls++
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			w.Header().Set("Link", "<"+l.in.Settings.APIHost+"/api/v3/app/installations?page=2>; rel=\"next\"")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		l.fake.Handler().ServeHTTP(w, r)
	})
	_, err := github.NewOver(memory{handler}).Repositories(t.Context(), l.in)
	limited, ok := errors.AsType[*port.RateLimited](err)
	if !ok || !limited.Reset.Equal(reset) || calls != 1 {
		t.Fatal("SDK's cached valid reset became a permission error or sent another request", err, calls)
	}
}
