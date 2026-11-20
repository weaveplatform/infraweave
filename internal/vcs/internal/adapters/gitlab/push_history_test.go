package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushHistory(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistory(t, l.gl, l.repo.Repo, l.fake.Repo("acme/platform", "infra"))
}

func TestUnitPushHistoryPages(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistoryPages(t, l.gl, l.repo.Repo, l.fake.Repo("acme/platform", "infra"))
}

func TestUnitPushHistoryOperationalFailures(t *testing.T) {
	for _, mode := range []string{"rate limit", "server failure", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			l := newLab(t)
			fault := gitlabfake.ServerError
			ctx := t.Context()
			if mode == "rate limit" {
				fault = gitlabfake.TooManyRequests
			}
			if mode == "deadline" {
				fault = gitlabfake.Hang
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
			}
			l.fake.FailPath("/projects/acme/platform/infra/repository/commits", fault, time.Minute)
			got, err := l.gl.RecoverPush(ctx, l.repo.Repo, l.repo.Base, l.repo.Head)
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
	sourcetest.PushHistoryDeadlines(t, func(rt http.RoundTripper) port.PushHistory { return gitlab.NewOver(rt) }, l.repo, gitlab.Timeout)
}
