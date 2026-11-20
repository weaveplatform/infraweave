package github

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.PushHistory = (*Provider)(nil)

// RecoverPush pages the native ancestry range independently of compare's
// 300-file limit, then compares complete trees for each commit's first parent.
func (p *Provider) RecoverPush(ctx context.Context, repo port.Repository, base, head string) (port.PushRange, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, err := p.repo(ctx, repo)
	if err != nil {
		return port.PushRange{}, err
	}
	newBranch := port.ZeroCommit(base)
	if newBranch {
		metadata, _, err := c.Repositories.Get(ctx, repo.Namespace, repo.Name)
		if err != nil {
			return port.PushRange{}, classify(err, "push default branch")
		}
		base = metadata.GetDefaultBranch()
		if base == "" {
			return port.PushRange{}, fmt.Errorf("%w: github: missing push default branch", port.ErrNotFound)
		}
	}
	commits, ancestor, err := pushCommits(ctx, c, repo, base, head)
	if err != nil {
		return port.PushRange{}, err
	}
	if newBranch {
		base = ancestor
	}
	trees := make(map[string]map[string]treeFile)
	items := make([]vcsapi.PushCommit, 0, len(commits))
	for _, commit := range commits {
		if commit == nil || commit.Parents == nil || !port.ValidCommit(commit.GetSHA()) {
			return port.PushRange{}, fmt.Errorf("%w: github: incomplete push commit", kernel.ErrUnavailable)
		}
		current, err := pushCommitTree(ctx, c, repo, commit.GetSHA(), trees)
		if err != nil {
			return port.PushRange{}, err
		}
		previous := map[string]treeFile{}
		if len(commit.Parents) > 0 {
			previous, err = pushCommitTree(ctx, c, repo, commit.Parents[0].GetSHA(), trees)
			if err != nil {
				return port.PushRange{}, err
			}
		}
		author := commit.GetAuthor().GetLogin()
		if author == "" {
			author = commit.GetCommit().GetAuthor().GetName()
		}
		items = append(items, vcsapi.PushCommit{
			Hash: commit.GetSHA(), Author: author,
			Message: commit.GetCommit().GetMessage(), URL: commit.GetHTMLURL(), At: commit.GetCommit().GetAuthor().GetDate().Time,
			Files: new(changedTreePaths(previous, current)),
		})
	}
	result, err := port.RecoveredRange(base, "github/REST compare commits and complete trees", items)
	if err != nil {
		return port.PushRange{}, fmt.Errorf("github: recover push range: %w", err)
	}
	return result, nil
}

func pushCommits(ctx context.Context, client *github.Client, repo port.Repository, base, head string) ([]*github.RepositoryCommit, string, error) {
	var out []*github.RepositoryCommit
	ancestor, total := "", -1
	for page := 1; ; {
		comparison, response, err := client.Repositories.CompareCommits(ctx, repo.Namespace, repo.Name, base, head, &github.ListOptions{Page: page, PerPage: 100})
		if err != nil {
			if page == 1 && errors.Is(classify(err, "push comparison"), port.ErrNotFound) && !port.ZeroCommit(base) {
				return unrelatedPushCommits(ctx, client, repo, base, head)
			}
			return nil, "", classify(err, "push comparison")
		}
		if comparison == nil || response == nil || comparison.TotalCommits == nil || total >= 0 && total != comparison.GetTotalCommits() {
			return nil, "", fmt.Errorf("%w: github: incomplete push commit count", kernel.ErrUnavailable)
		}
		total = comparison.GetTotalCommits()
		if page == 1 {
			ancestor = comparison.GetMergeBaseCommit().GetSHA()
		}
		out = append(out, comparison.Commits...)
		if len(out) > total || response.NextPage != 0 && response.NextPage <= page {
			return nil, "", fmt.Errorf("%w: github: incomplete push pagination", kernel.ErrUnavailable)
		}
		if response.NextPage == 0 {
			if len(out) != total {
				return nil, "", fmt.Errorf("%w: github: truncated push commits", kernel.ErrUnavailable)
			}
			return out, ancestor, nil
		}
		page = response.NextPage
	}
}

// GitHub returns 404 when existing commits share no ancestor. Enumerating
// both immutable histories also distinguishes that case from a missing ref.
func unrelatedPushCommits(ctx context.Context, client *github.Client, repo port.Repository, base, head string) ([]*github.RepositoryCommit, string, error) {
	before, err := pushHistory(ctx, client, repo, base)
	if err != nil {
		return nil, "", err
	}
	after, err := pushHistory(ctx, client, repo, head)
	if err != nil {
		return nil, "", err
	}
	excluded := make(map[string]bool, len(before))
	for _, commit := range before {
		excluded[commit.GetSHA()] = true
	}
	out := make([]*github.RepositoryCommit, 0, len(after))
	for _, commit := range after {
		if !excluded[commit.GetSHA()] {
			out = append(out, commit)
		}
	}
	return out, "", nil
}

func pushHistory(ctx context.Context, client *github.Client, repo port.Repository, sha string) ([]*github.RepositoryCommit, error) {
	out := []*github.RepositoryCommit{}
	seen := map[string]bool{}
	for page := 1; ; {
		commits, response, err := client.Repositories.ListCommits(
			ctx,
			repo.Namespace,
			repo.Name,
			&github.CommitsListOptions{SHA: sha, ListOptions: github.ListOptions{Page: page, PerPage: 100}},
		)
		if err != nil {
			return nil, classify(err, "push history")
		}
		for _, commit := range commits {
			if !port.ValidCommit(commit.GetSHA()) || seen[commit.GetSHA()] {
				return nil, fmt.Errorf("%w: github: invalid push history", kernel.ErrUnavailable)
			}
			seen[commit.GetSHA()] = true
		}
		out = append(out, commits...)
		if response.NextPage == 0 {
			if !seen[sha] {
				return nil, fmt.Errorf("%w: github: push history omits requested commit", kernel.ErrUnavailable)
			}
			return out, nil
		}
		if response.NextPage <= page {
			return nil, fmt.Errorf("%w: github: cyclic push history", kernel.ErrUnavailable)
		}
		page = response.NextPage
	}
}

func pushCommitTree(ctx context.Context, client *github.Client, repo port.Repository, sha string, cache map[string]map[string]treeFile) (map[string]treeFile, error) {
	if tree, exists := cache[sha]; exists {
		return tree, nil
	}
	commit, _, err := client.Repositories.GetCommit(ctx, repo.Namespace, repo.Name, sha, nil)
	if err != nil {
		return nil, classify(err, "push commit tree")
	}
	if commit.GetSHA() != sha || !port.ValidCommit(commit.GetCommit().GetTree().GetSHA()) {
		return nil, fmt.Errorf("%w: github: invalid push commit tree", kernel.ErrUnavailable)
	}
	tree, err := treeFiles(ctx, client, repo, commit.GetCommit().GetTree().GetSHA())
	if err == nil {
		cache[sha] = tree
	}
	return tree, err
}
