package azuredevops_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushDiff(t *testing.T) {
	l := newLab(t)
	sourcetest.Push(t, l.az, l.repo.Repo, l.fake.Repo("Platform", "infra"))
}
