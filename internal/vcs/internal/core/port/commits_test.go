package port

import (
	"slices"
	"strings"
	"testing"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func TestUnitNativeCommitsKeepOrderAndRefuseIncompleteFacts(t *testing.T) {
	files := []string{"b.tf", "a.tf", "a.tf"}
	valid := vcsapi.PushCommit{Hash: strings.Repeat("a", 40), At: time.Now(), Files: &files}
	for _, mode := range []string{"complete", "missing files", "missing time", "missing hash", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			item := valid
			reason, want := "", ""
			switch mode {
			case "missing files":
				item.Files = nil
				want = "missing-commit-files"
			case "missing time":
				item.At = time.Time{}
				want = "incomplete-commit-metadata"
			case "missing hash":
				item.Hash = ""
				want = "incomplete-commit-metadata"
			case "truncated":
				reason = "provider-commit-limit"
				want = reason
			}
			got := NativeCommits("native.commits", []vcsapi.PushCommit{item}, reason)
			if got.Source != "native.commits" || got.Omission != want || len(got.Items) != 1 || got.Items[0].Hash != item.Hash {
				t.Fatal(got)
			}
			if item.Files != nil && (!slices.Equal(*got.Items[0].Files, []string{"a.tf", "b.tf"}) || !slices.Equal(files, []string{"b.tf", "a.tf", "a.tf"})) {
				t.Fatal("native paths changed or aliased", got)
			}
		})
	}
}

func TestUnitNativeCommitsRefuseRepeatedObjectIdentities(t *testing.T) {
	item := vcsapi.PushCommit{Hash: strings.Repeat("a", 40), At: time.Now(), Files: new([]string{"main.tf"})}
	other := item
	other.Hash = strings.ToUpper(item.Hash)
	items := []vcsapi.PushCommit{item, other}
	got := NativeCommits("signed commits", items, "")
	if got.Omission != "repeated-commit" || len(got.Items) != 2 {
		t.Fatal("duplicate native objects became a complete collection", got)
	}
	if _, complete := NativeCommitFiles(&vcsapi.CommitCollection{Source: "legacy", Items: items}); complete {
		t.Fatal("legacy duplicate collection bypassed completeness validation")
	}
}
