package gitlab

import (
	"context"
	"fmt"
	"slices"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.PushHistory = (*Provider)(nil)

// RecoverPush uses the paged revision-range log and complete repository trees;
// comparison diffs and per-commit diff lists can hit GitLab's diff limits.
func (p *Provider) RecoverPush(ctx context.Context, repo port.Repository, base, head string) (port.PushRange, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, h, pid, err := p.repo(repo)
	if err != nil {
		return port.PushRange{}, err
	}
	base, err = pushBase(ctx, c, pid, base, head)
	if err != nil {
		return port.PushRange{}, err
	}
	ref := base + ".." + head
	commits, err := pushPages("push range commits", func(page int64) ([]*gitlab.Commit, *gitlab.Response, error) {
		return c.Commits.ListCommits(pid, &gitlab.ListCommitsOptions{
			RefName:     &ref,
			ListOptions: gitlab.ListOptions{Page: page, PerPage: 100},
		}, gitlab.WithContext(ctx))
	})
	if err != nil {
		return port.PushRange{}, err
	}
	trees := make(map[string]map[string]pushTreeFile)
	items := make([]vcsapi.PushCommit, 0, len(commits))
	for _, commit := range commits {
		if commit == nil || commit.ParentIDs == nil || commit.AuthoredDate == nil {
			return port.PushRange{}, fmt.Errorf("%w: gitlab: incomplete push commit", kernel.ErrUnavailable)
		}
		current, err := pushTree(ctx, c, pid, commit.ID, trees)
		if err != nil {
			return port.PushRange{}, err
		}
		previous := map[string]pushTreeFile{}
		if len(commit.ParentIDs) > 0 {
			previous, err = pushTree(ctx, c, pid, commit.ParentIDs[0], trees)
			if err != nil {
				return port.PushRange{}, err
			}
		}
		items = append(items, vcsapi.PushCommit{
			Hash: commit.ID, Author: commit.AuthorName, Message: commit.Message,
			URL: h.web + "/" + pid + "/-/commit/" + commit.ID, At: *commit.AuthoredDate,
			Files: new(pushTreeChanges(previous, current)),
		})
	}
	result, err := port.RecoveredRange(base, "gitlab/REST revision-range commits and complete trees", items)
	if err != nil {
		return port.PushRange{}, fmt.Errorf("gitlab: recover push range: %w", err)
	}
	return result, nil
}

func pushBase(ctx context.Context, c *gitlab.Client, pid, base, head string) (string, error) {
	if !port.ZeroCommit(base) {
		return base, nil
	}
	project, response, err := c.Projects.GetProject(pid, nil, gitlab.WithContext(ctx))
	if err != nil {
		return "", classify(response, err, "push default branch")
	}
	if project == nil || project.DefaultBranch == "" {
		return "", fmt.Errorf("%w: gitlab: missing push default branch", port.ErrNotFound)
	}
	ancestor, response, err := c.Repositories.MergeBase(pid, &gitlab.MergeBaseOptions{Ref: new([]string{project.DefaultBranch, head})}, gitlab.WithContext(ctx))
	if err != nil {
		return "", classify(response, err, "push default merge base")
	}
	if ancestor == nil || !port.ValidCommit(ancestor.ID) {
		return "", fmt.Errorf("%w: gitlab: missing push merge base", kernel.ErrUnavailable)
	}
	return ancestor.ID, nil
}

type pushTreeFile struct{ id, mode, kind string }

func pushTree(ctx context.Context, c *gitlab.Client, pid, ref string, cache map[string]map[string]pushTreeFile) (map[string]pushTreeFile, error) {
	if tree, exists := cache[ref]; exists {
		return tree, nil
	}
	if !port.ValidCommit(ref) {
		return nil, fmt.Errorf("%w: gitlab: invalid push tree ref", kernel.ErrUnavailable)
	}
	entries, err := pushPages("push repository tree", func(page int64) ([]*gitlab.TreeNode, *gitlab.Response, error) {
		return c.Repositories.ListTree(pid, &gitlab.ListTreeOptions{
			Ref: &ref, Recursive: new(true),
			ListOptions: gitlab.ListOptions{Page: page, PerPage: 100},
		}, gitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, err
	}
	tree := make(map[string]pushTreeFile, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Path == "" || !port.ValidCommit(entry.ID) || entry.Mode == "" || entry.Type == "" {
			return nil, fmt.Errorf("%w: gitlab: incomplete push tree entry", kernel.ErrUnavailable)
		}
		if entry.Type == "tree" {
			continue
		}
		if _, exists := tree[entry.Path]; exists {
			return nil, fmt.Errorf("%w: gitlab: repeated push tree entry", kernel.ErrUnavailable)
		}
		tree[entry.Path] = pushTreeFile{entry.ID, entry.Mode, entry.Type}
	}
	cache[ref] = tree
	return tree, nil
}

func pushTreeChanges(before, after map[string]pushTreeFile) []string {
	paths := make([]string, 0, len(before)+len(after))
	for name, file := range before {
		if other, exists := after[name]; !exists || other != file {
			paths = append(paths, name)
		}
	}
	for name, file := range after {
		if other, exists := before[name]; !exists || other != file {
			paths = append(paths, name)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func (p *Provider) pushAffectedFiles(ctx context.Context, repo port.Repository, base, head string) ([]string, error) {
	c, _, pid, err := p.repo(repo)
	if err != nil {
		return nil, err
	}
	base, err = pushBase(ctx, c, pid, base, head)
	if err != nil {
		return nil, err
	}
	cache := make(map[string]map[string]pushTreeFile)
	before, err := pushTree(ctx, c, pid, base, cache)
	if err != nil {
		return nil, err
	}
	after, err := pushTree(ctx, c, pid, head, cache)
	if err != nil {
		return nil, err
	}
	return pushTreeChanges(before, after), nil
}

// A full page without a next header is ambiguous. Read one further offset
// page rather than silently treating a provider's omitted header as completion.
func pushPages[T any](what string, read func(int64) ([]T, *gitlab.Response, error)) ([]T, error) {
	out := []T{}
	for page := int64(1); ; {
		items, response, err := read(page)
		if err != nil {
			return nil, classify(response, err, what)
		}
		if items == nil || response == nil || response.NextPage != 0 && (response.NextPage <= page || len(items) == 0) {
			return nil, fmt.Errorf("%w: gitlab: incomplete %s pagination", kernel.ErrUnavailable, what)
		}
		out = append(out, items...)
		switch {
		case response.NextPage != 0:
			page = response.NextPage
		case len(items) == 100:
			page++
		default:
			return out, nil
		}
	}
}
