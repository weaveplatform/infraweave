package azuredevops_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushHistory(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistory(t, l.az, l.repo.Repo, l.fake.Repo("Platform", "infra"))
}

func TestUnitPushHistoryPages(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistoryPages(t, l.az, l.repo.Repo, l.fake.Repo("Platform", "infra"))
}

func TestUnitPushHistoryOperationalFailures(t *testing.T) {
	for _, mode := range []string{"rate limit", "server failure", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			l := newLab(t)
			fault := azuredevopsfake.ServerError
			ctx := t.Context()
			if mode == "rate limit" {
				fault = azuredevopsfake.TooManyRequests
			}
			if mode == "deadline" {
				fault = azuredevopsfake.Hang
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
			}
			l.fake.FailPath("/Platform/_apis/git/repositories/infra/commits", fault, time.Minute)
			got, err := l.az.RecoverPush(ctx, l.repo.Repo, l.repo.Base, l.repo.Head)
			if got.Commits != nil || err == nil {
				t.Fatal("failed native recovery became a complete range", got, err)
			}
			switch mode {
			case "rate limit":
				if _, ok := errors.AsType[*port.RateLimited](err); !ok {
					t.Fatal("native rate limit was lost", err)
				}
			case "server failure":
				if !errors.Is(err, kernel.ErrUnavailable) {
					t.Fatal(err)
				}
			case "deadline":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("native deadline was lost", err)
				}
			}
		})
	}
}

func TestUnitPushHistoryPreservesDeadlineCauses(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistoryDeadlines(t, func(rt http.RoundTripper) port.PushHistory { return azuredevops.NewOver(rt) }, l.repo, azuredevops.Timeout)
}
