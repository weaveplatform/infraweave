package bitbucketdc_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushHistory(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistory(t, l.bb, l.repo.Repo, l.fake.Repo("ACME", "infra"))
}

func TestUnitPushHistoryPages(t *testing.T) {
	l := newLab(t)
	sourcetest.PushHistoryPages(t, l.bb, l.repo.Repo, l.fake.Repo("ACME", "infra"))
}
