package port

import (
	"slices"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// NativeCommits retains native order and annotates missing required policy
// facts. Adapter-supplied truncation or attribution reasons take precedence.
func NativeCommits(source string, items []vcsapi.PushCommit, omission string) *vcsapi.CommitCollection {
	out := &vcsapi.CommitCollection{Source: source, Omission: omission, Items: slices.Clone(items)}
	for i, commit := range out.Items {
		if (!ValidCommit(commit.Hash) || commit.At.IsZero()) && out.Omission == "" {
			out.Omission = "incomplete-commit-metadata"
		}
		if commit.Files == nil {
			if out.Omission == "" {
				out.Omission = "missing-commit-files"
			}
			continue
		}
		files := slices.Clone(*commit.Files)
		slices.Sort(files)
		out.Items[i].Files = new(slices.Compact(files))
	}
	return out
}

// NativeCommitFiles returns the touched-path union only when every native
// commit has complete required facts. An intermediate edit and revert is still
// relevant even if the before/head trees are equal.
func NativeCommitFiles(native *vcsapi.CommitCollection) ([]string, bool) {
	if native == nil || native.Omission != "" {
		return nil, false
	}
	out := []string{}
	for _, item := range native.Items {
		if !ValidCommit(item.Hash) || item.At.IsZero() || item.Files == nil {
			return nil, false
		}
		out = append(out, (*item.Files)...)
	}
	slices.Sort(out)
	return slices.Compact(out), true
}
