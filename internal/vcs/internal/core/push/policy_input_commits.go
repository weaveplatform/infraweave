package push

import (
	"slices"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func pushCommitFacts(native *vcsapi.CommitCollection, branch string) policyapi.Fact[[]policyapi.PushCommitFacts] {
	if _, complete := port.NativeCommitFiles(native); !complete {
		return policyapi.Fact[[]policyapi.PushCommitFacts]{}
	}
	out := make([]policyapi.PushCommitFacts, 0, len(native.Items))
	for _, item := range native.Items {
		commit := policyapi.CommitFacts{Hash: known(item.Hash), Author: item.Author, Message: item.Message, At: item.At, Branch: branch}
		if item.URL != "" {
			commit.URL = known(item.URL)
		}
		out = append(out, policyapi.PushCommitFacts{Commit: commit, AffectedFiles: slices.Clone(*item.Files)})
	}
	return known(out)
}

func pushAffectedFiles(files []string, native *vcsapi.CommitCollection) []string {
	out := slices.Clone(files)
	if paths, complete := port.NativeCommitFiles(native); complete {
		out = append(out, paths...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
