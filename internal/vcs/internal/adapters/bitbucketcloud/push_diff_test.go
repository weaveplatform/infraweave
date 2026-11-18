package bitbucketcloud_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushDiff(t *testing.T) {
	l := newLab(t)
	sourcetest.Push(t, l.bb, l.repo.Repo, l.fake.Repo("acme", "infra"))
}
