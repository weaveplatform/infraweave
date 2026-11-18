package github_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushDiff(t *testing.T) {
	l := newLab(t)
	sourcetest.Push(t, l.gh, l.repo.Repo, l.fake.Repo("acme", "infra"))
}

func TestUnitPushCompleteTrees(t *testing.T) {
	for _, truncate := range []bool{false, true} {
		t.Run(fmt.Sprintf("recursive truncated=%t", truncate), func(t *testing.T) {
			l := newLab(t)
			r := l.fake.Repo("acme", "infra")
			files := map[string]string{}
			for i := range 350 {
				files[fmt.Sprintf("many/group%d/file%03d.tf", i%5, i)] = "before"
			}
			before := r.Commit("main", "more than compare's first 300 files", files)
			for name := range files {
				files[name] = "after"
			}
			head := r.Commit("main", "every file changes", files)
			if truncate {
				l.fake.TruncateRecursiveTrees(2)
			}
			got, err := l.gh.AffectedFiles(t.Context(), l.repo.Repo, before, head, port.PushDiff)
			if err != nil || len(got) != len(files) || !slices.Contains(got, "many/group4/file349.tf") {
				t.Fatal("push comparison lost paths at a provider limit", len(got), err)
			}
		})
	}
}
