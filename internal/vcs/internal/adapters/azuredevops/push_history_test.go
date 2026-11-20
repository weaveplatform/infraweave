package azuredevops_test

import (
	"testing"

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
