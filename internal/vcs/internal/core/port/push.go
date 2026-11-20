package port

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// PushHistory recovers a complete immutable ancestry range when the signed
// webhook cannot supply it. Raw Git has no webhook history to recover.
type PushHistory interface {
	RecoverPush(ctx context.Context, repo Repository, base, head string) (PushRange, error)
}

// PushRange freezes the default-branch merge base for a new branch alongside
// the recovered facts, so the later tree comparison cannot follow a moved ref.
type PushRange struct {
	Base    string
	Commits *vcsapi.CommitCollection
}

// RecoveredRange refuses partial or ambiguous metadata before it can become
// policy input. Empty ranges are valid for unchanged and backward force pushes.
func RecoveredRange(base, source string, items []vcsapi.PushCommit) (PushRange, error) {
	native := NativeCommits(source, items, "")
	if _, complete := NativeCommitFiles(native); !ValidCommit(base) || !complete || source == "" {
		return PushRange{}, fmt.Errorf("%w: incomplete recovered push range", kernel.ErrUnavailable)
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item.Hash] {
			return PushRange{}, fmt.Errorf("%w: repeated recovered push commit", kernel.ErrUnavailable)
		}
		seen[item.Hash] = true
	}
	return PushRange{Base: base, Commits: native}, nil
}

// RecoveredCommit carries the same preferred author identity as a resolved
// commit; a present empty file list proves an actual empty commit.
func RecoveredCommit(commit vcsapi.Commit, files []string) vcsapi.PushCommit {
	if files == nil {
		files = []string{}
	}
	author := commit.AuthorName
	if commit.AuthorLogin != nil && *commit.AuthorLogin != "" {
		author = *commit.AuthorLogin
	}
	return vcsapi.PushCommit{
		Hash: commit.Hash, Author: author, Message: commit.Message,
		URL: commit.URL, At: commit.Timestamp, Files: &files,
	}
}

// PreparedPush carries immutable source-owner facts into the dispatcher. REST
// recovery is separate from the original signed native collection, which the
// normalized event retains even when the provider supplied a truncated list.
type PreparedPush struct {
	Commit           stacksapi.Commit
	Files            []string
	RecoveredCommits *vcsapi.CommitCollection
}
