package gitlab_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushDiff(t *testing.T) {
	l := newLab(t)
	sourcetest.Push(t, l.gl, l.repo.Repo, l.fake.Repo("acme/platform", "infra"))
}
