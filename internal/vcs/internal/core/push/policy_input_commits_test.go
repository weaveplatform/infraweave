package push

import (
	"strings"
	"testing"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func TestUnitPolicyPushCommitsKeepUnknownFactsAbsent(t *testing.T) {
	files := []string{"a.tf"}
	for _, mode := range []string{"complete", "missing", "truncated", "no files", "no timestamp", "bad hash", "empty"} {
		t.Run(mode, func(t *testing.T) {
			c := &vcsapi.CommitCollection{
				Items: []vcsapi.PushCommit{{Hash: strings.Repeat("a", 40), Author: "author", Message: "message", URL: "https://example.com/c", At: time.Now(), Files: &files}},
			}
			switch mode {
			case "missing":
				c = nil
			case "truncated":
				c.Omission = "provider-commit-limit"
			case "no files":
				c.Items[0].Files = nil
			case "no timestamp":
				c.Items[0].At = time.Time{}
			case "bad hash":
				c.Items[0].Hash = "unknown"
			case "empty":
				c.Items = []vcsapi.PushCommit{}
			}
			got := pushCommitFacts(c, "feature")
			valid := mode == "complete" || mode == "empty"
			if got.Known != valid || (got.Value != nil) != valid {
				t.Fatal(got)
			}
			if mode == "complete" {
				p := (*got.Value)[0]
				if p.Commit.Branch != "feature" || p.Commit.Author != "author" || p.Commit.URL.Value == nil || *p.Commit.URL.Value != "https://example.com/c" {
					t.Fatal(got)
				}
				p.AffectedFiles[0] = "changed"
				if files[0] != "a.tf" {
					t.Fatal("policy facts alias native stored paths")
				}
			}
		})
	}
}
