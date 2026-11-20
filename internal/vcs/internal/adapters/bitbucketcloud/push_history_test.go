package bitbucketcloud_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushHistory(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistory(t, l.bb, l.repo.Repo, l.fake.Repo("acme", "infra"))
}

func TestUnitPushHistoryPages(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistoryPages(t, l.bb, l.repo.Repo, l.fake.Repo("acme", "infra"))
}

func TestUnitPushHistoryOperationalFailures(t *testing.T) {
	for _, mode := range []string{"rate limit", "server failure", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			l := newLab(t)
			fault := bitbucketcloudfake.ServerError
			ctx := t.Context()
			if mode == "rate limit" {
				fault = bitbucketcloudfake.TooManyRequests
			}
			if mode == "deadline" {
				fault = bitbucketcloudfake.Hang
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
			}
			l.fake.FailPath("/repositories/acme/infra/commits", fault, time.Minute)
			got, err := l.bb.RecoverPush(ctx, l.repo.Repo, l.repo.Base, l.repo.Head)
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
