package port

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func TestUnitRecoveredRangeRejectsPartialFacts(t *testing.T) {
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	valid := vcsapi.PushCommit{Hash: head, At: time.Now(), Files: new([]string{"main.tf"})}
	for _, mode := range []string{"complete", "empty", "invalid base", "missing files", "missing date", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			anchor, items := base, []vcsapi.PushCommit{valid}
			switch mode {
			case "empty":
				items = nil
			case "invalid base":
				anchor = "main"
			case "missing files":
				items[0].Files = nil
			case "missing date":
				items[0].At = time.Time{}
			case "duplicate":
				items = append(items, valid)
			}
			got, err := RecoveredRange(anchor, "REST range", items)
			if mode == "complete" || mode == "empty" {
				if err != nil || got.Base != base || got.Commits.Source != "REST range" || len(got.Commits.Items) != len(items) {
					t.Fatal(got, err)
				}
			} else if !errors.Is(err, kernel.ErrUnavailable) || got.Commits != nil {
				t.Fatal("partial range became usable", got, err)
			}
		})
	}
}

func TestUnitRecoveredCommitKeepsNativeIdentityAndExplicitEmptyPaths(t *testing.T) {
	commit := vcsapi.Commit{Hash: strings.Repeat("a", 40), AuthorName: "Name", Message: "Message", URL: "https://git.example/commit", Timestamp: time.Now()}
	for _, login := range []*string{nil, new(""), new("native-login")} {
		commit.AuthorLogin = login
		got := RecoveredCommit(commit, []string{})
		want := "Name"
		if login != nil && *login != "" {
			want = *login
		}
		if got.Author != want || got.At != commit.Timestamp || got.Hash != commit.Hash || got.Message != commit.Message || got.URL != commit.URL || got.Files == nil ||
			len(*got.Files) != 0 {
			t.Fatal(got)
		}
	}
}
