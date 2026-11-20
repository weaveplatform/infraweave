package sourcetest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// PushHistory checks actual Git ancestry independently of native pagination or
// comparison implementations, including paths canceled out by endpoint trees.
func PushHistory(t *testing.T, source port.PushHistory, repo port.Repository, history interface {
	History
	Orphan(string, string, string) string
},
) {
	t.Helper()
	base := history.Commit("trunk", "range base", map[string]string{"root/main.tf": "before", "old.txt": "remove"})
	history.SetDefaultBranch("trunk")
	edit := history.Commit("trunk", "intermediate edit", map[string]string{"root/main.tf": "edited"})
	reverted := history.Commit("trunk", "revert to original tree", map[string]string{"root/main.tf": "before"})
	renamed := history.Commit("trunk", "rename and delete", map[string]string{"root/main.tf": "", "root/renamed.tf": "before", "old.txt": ""})
	history.Branch("topic", base)
	topic := history.Commit("topic", "independent ref", map[string]string{"topic.txt": "topic"})
	orphan := history.Orphan("replacement", base, "independent history")
	for _, check := range []struct {
		name, before, head, anchor string
		commits                    int
		files                      []string
	}{
		{"intermediate edit and revert", base, reverted, base, 2, []string{"root/main.tf"}},
		{"rename and delete", reverted, renamed, reverted, 1, []string{"old.txt", "root/main.tf", "root/renamed.tf"}},
		{"new branch freezes default merge base", strings.Repeat("0", 40), topic, base, 1, []string{"topic.txt"}},
		{"divergent force push excludes old ancestors", renamed, topic, renamed, 1, []string{"topic.txt"}},
		{"backward force push has no newly reachable commits", reverted, base, reverted, 0, nil},
		{"same commit has empty range", edit, edit, edit, 0, nil},
		{"unrelated history includes real root paths", base, orphan, base, 1, []string{"README.md", "main.tf", "modules/new.tf", "old.txt", "root/main.tf"}},
	} {
		t.Run(check.name, func(t *testing.T) {
			got, err := source.RecoverPush(t.Context(), repo, check.before, check.head)
			if err != nil {
				t.Fatal(err)
			}
			files, complete := port.NativeCommitFiles(got.Commits)
			if !complete || got.Base != check.anchor || got.Commits.Source == "" || len(got.Commits.Items) != check.commits || !slices.Equal(files, check.files) {
				t.Fatalf("range = %+v, %q, complete=%t; want base=%s, %d commits, %q", got, files, complete, check.anchor, check.commits, check.files)
			}
			for _, commit := range got.Commits.Items {
				if commit.Author == "" || commit.Message == "" || commit.URL == "" || commit.Hash == check.before {
					t.Fatalf("recovery omitted or misattributed native commit facts: %+v", commit)
				}
			}
		})
	}
	if _, err := source.RecoverPush(t.Context(), port.Repository{}, base, reverted); !errors.Is(err, port.ErrNoIntegration) {
		t.Errorf("missing original integration = %v", err)
	}
	if _, err := source.RecoverPush(t.Context(), repo, base, strings.Repeat("f", 40)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("missing immutable head = %v", err)
	}
}

// PushHistoryPages crosses normal native commit-page and file-page sizes with
// real commits and trees, not precomputed provider response arrays.
func PushHistoryPages(t *testing.T, source port.PushHistory, repo port.Repository, history History) {
	t.Helper()
	base := history.Commit("pages", "paged range base", map[string]string{"page.tf": "base"})
	head := base
	for i := range 105 {
		head = history.Commit("pages", fmt.Sprintf("paged change %03d", i), map[string]string{"page.tf": fmt.Sprint(i)})
	}
	got, err := source.RecoverPush(t.Context(), repo, base, head)
	if err != nil || got.Commits == nil || len(got.Commits.Items) != 105 {
		t.Fatalf("paged range lost commits: %+v, %v", got, err)
	}
	files, complete := port.NativeCommitFiles(got.Commits)
	if !complete || !slices.Equal(files, []string{"page.tf"}) {
		t.Fatalf("paged range paths = %q, complete=%t", files, complete)
	}
	paths := map[string]string{}
	for i := range 205 {
		paths[fmt.Sprintf("many/file%03d.tf", i)] = "new"
	}
	after := history.Commit("pages", "more than two file pages", paths)
	got, err = source.RecoverPush(t.Context(), repo, head, after)
	if err != nil || got.Commits == nil || len(got.Commits.Items) != 1 {
		t.Fatalf("paged files lost commit: %+v, %v", got, err)
	}
	files, complete = port.NativeCommitFiles(got.Commits)
	if !complete || len(files) != 205 || !slices.Contains(files, "many/file204.tf") {
		t.Fatalf("paged range lost paths: count=%d complete=%t", len(files), complete)
	}
}
