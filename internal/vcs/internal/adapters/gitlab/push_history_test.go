package gitlab_test

import (
	"testing"

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
