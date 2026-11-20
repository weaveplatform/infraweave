package github_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushHistory(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistory(t, l.gh, l.repo.Repo, l.fake.Repo("acme", "infra"))
}

func TestUnitPushHistoryPages(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistoryPages(t, l.gh, l.repo.Repo, l.fake.Repo("acme", "infra"))
}
