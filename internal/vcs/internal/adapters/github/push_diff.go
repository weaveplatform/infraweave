package github

import (
	"context"
	"fmt"
	"path"
	"slices"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type treeFile struct{ sha, mode, kind string }

func (p *Provider) pushAffectedFiles(ctx context.Context, repo port.Repository, base, head string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, err := p.repo(ctx, repo)
	if err != nil {
		return nil, err
	}
	if port.ZeroCommit(base) {
		metadata, _, err := c.Repositories.Get(ctx, repo.Namespace, repo.Name)
		if err != nil {
			return nil, classify(err, "default branch")
		}
		if metadata.GetDefaultBranch() == "" {
			return nil, fmt.Errorf("%w: github: default branch", port.ErrNotFound)
		}
		comparison, _, err := c.Repositories.CompareCommits(ctx, repo.Namespace, repo.Name, metadata.GetDefaultBranch(), head, nil)
		if err != nil {
			return nil, classify(err, "default branch merge base")
		}
		base = comparison.GetMergeBaseCommit().GetSHA()
		if base == "" {
			return nil, fmt.Errorf("%w: github: comparison has no merge base", kernel.ErrUnavailable)
		}
	}
	files := make([]map[string]treeFile, 0, 2)
	for _, ref := range []string{base, head} {
		commit, _, err := c.Repositories.GetCommit(ctx, repo.Namespace, repo.Name, ref, nil)
		if err != nil {
			return nil, classify(err, ref)
		}
		tree := commit.GetCommit().GetTree().GetSHA()
		if tree == "" {
			return nil, fmt.Errorf("%w: github: commit has no tree", kernel.ErrUnavailable)
		}
		entries, err := treeFiles(ctx, c, repo, tree)
		if err != nil {
			return nil, err
		}
		files = append(files, entries)
	}
	changed := map[string]bool{}
	for index, entries := range files {
		for name, file := range entries {
			if other, exists := files[1-index][name]; !exists || file != other {
				changed[name] = true
			}
		}
	}
	paths := make([]string, 0, len(changed))
	for name := range changed {
		paths = append(paths, name)
	}
	slices.Sort(paths)
	return paths, nil
}

func treeFiles(ctx context.Context, client *github.Client, repo port.Repository, sha string) (map[string]treeFile, error) {
	tree, _, err := client.Git.GetTree(ctx, repo.Namespace, repo.Name, sha, true)
	if err != nil {
		return nil, classify(err, "tree "+sha)
	}
	files := map[string]treeFile{}
	if !tree.GetTruncated() {
		for _, entry := range tree.Entries {
			if entry.GetType() != "tree" {
				files[entry.GetPath()] = treeFile{entry.GetSHA(), entry.GetMode(), entry.GetType()}
			}
		}
		return files, nil
	}
	// A recursive tree can truncate at GitHub's response limit. Ignore that
	// partial list and walk complete single-level trees instead.
	type pending struct {
		sha, prefix string
		ancestors   map[string]bool
	}
	work := []pending{{sha: sha, ancestors: map[string]bool{}}}
	for len(work) > 0 {
		item := work[len(work)-1]
		work = work[:len(work)-1]
		if item.ancestors[item.sha] {
			return nil, fmt.Errorf("%w: github: cyclic tree", kernel.ErrUnavailable)
		}
		ancestors := make(map[string]bool, len(item.ancestors)+1)
		for key := range item.ancestors {
			ancestors[key] = true
		}
		ancestors[item.sha] = true
		tree, _, err := client.Git.GetTree(ctx, repo.Namespace, repo.Name, item.sha, false)
		if err != nil {
			return nil, classify(err, "tree "+item.sha)
		}
		if tree.GetTruncated() {
			return nil, fmt.Errorf("%w: github: single-level tree truncated", kernel.ErrUnavailable)
		}
		for _, entry := range tree.Entries {
			name := path.Join(item.prefix, entry.GetPath())
			if entry.GetType() == "tree" {
				work = append(work, pending{sha: entry.GetSHA(), prefix: name, ancestors: ancestors})
			} else {
				files[name] = treeFile{entry.GetSHA(), entry.GetMode(), entry.GetType()}
			}
		}
	}
	return files, nil
}
