package push

import (
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
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

// preparedPushCommits preserves the signed collection while preferring complete
// recovered facts. Corrupt persisted recovery must fail, not become absence.
func preparedPushCommits(event Prepared) (policyapi.Fact[[]policyapi.PushCommitFacts], error) {
	collection := event.Event.Commits
	if event.RecoveredCommits != nil {
		collection = event.RecoveredCommits
		if _, complete := port.NativeCommitFiles(collection); !complete || collection.Source == "" {
			return policyapi.Fact[[]policyapi.PushCommitFacts]{}, fmt.Errorf("%w: invalid stored recovered push facts", kernel.ErrUnavailable)
		}
	}
	return pushCommitFacts(collection, event.Event.Branch), nil
}
