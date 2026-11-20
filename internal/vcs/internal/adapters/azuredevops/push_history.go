package azuredevops

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.PushHistory = (*Provider)(nil)

// RecoverPush subtracts complete immutable before ancestors from head ancestors.
// Azure's fromCommitId/toCommitId filters are alphabetical, not ancestry bounds.
// Reading each ref's documented paged history also attributes multi-ref pushes.
func (p *Provider) RecoverPush(ctx context.Context, repo port.Repository, base, head string) (port.PushRange, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return port.PushRange{}, err
	}
	o, err := orgOf(repo.Integration.Settings)
	if err != nil {
		return port.PushRange{}, err
	}
	base, err = p.pushBase(ctx, repo, rp, base, head)
	if err != nil {
		return port.PushRange{}, err
	}
	before, err := p.pushHistory(ctx, *repo.Integration, rp, base)
	if err != nil {
		return port.PushRange{}, err
	}
	after, err := p.pushHistory(ctx, *repo.Integration, rp, head)
	if err != nil {
		return port.PushRange{}, err
	}
	excluded := make(map[string]bool, len(before))
	for _, commit := range before {
		excluded[commit.CommitID] = true
	}
	items := make([]vcsapi.PushCommit, 0, len(after))
	for _, commit := range after {
		if excluded[commit.CommitID] {
			continue
		}
		files, err := p.pushCommitFiles(ctx, *repo.Integration, rp, commit.CommitID)
		if err != nil {
			return port.PushRange{}, err
		}
		items = append(items, port.RecoveredCommit(out(o, repo, commit), files))
	}
	result, err := port.RecoveredRange(base, "azuredevops/REST immutable histories and paged commit changes", items)
	if err != nil {
		return port.PushRange{}, fmt.Errorf("azuredevops: recover push range: %w", err)
	}
	return result, nil
}

func (p *Provider) pushHistory(ctx context.Context, in port.Integration, rp, ref string) ([]commit, error) {
	out := []commit{}
	seen := map[string]bool{}
	for skip := 0; ; {
		var page struct {
			Count *int      `json:"count"`
			Value *[]commit `json:"value"`
		}
		query := url.Values{
			"searchCriteria.itemVersion.version": {ref}, "searchCriteria.itemVersion.versionType": {"commit"},
			"searchCriteria.$top": {strconv.Itoa(top)}, "searchCriteria.$skip": {strconv.Itoa(skip)},
		}
		if _, err := p.decode(ctx, in, rp+"/commits", query, "push ancestry commits", &page); err != nil {
			return nil, err
		}
		if page.Value == nil || page.Count == nil || *page.Count != len(*page.Value) {
			return nil, fmt.Errorf("%w: azuredevops: incomplete push history page", kernel.ErrUnavailable)
		}
		for _, commit := range *page.Value {
			if !port.ValidCommit(commit.CommitID) || seen[commit.CommitID] {
				return nil, fmt.Errorf("%w: azuredevops: repeated or invalid push history commit", kernel.ErrUnavailable)
			}
			seen[commit.CommitID] = true
		}
		out = append(out, (*page.Value)...)
		if len(*page.Value) < top {
			if !seen[ref] {
				return nil, fmt.Errorf("%w: azuredevops: history omits requested push commit", kernel.ErrUnavailable)
			}
			return out, nil
		}
		skip += len(*page.Value)
	}
}

func (p *Provider) pushBase(ctx context.Context, repo port.Repository, rp, base, head string) (string, error) {
	if !port.ZeroCommit(base) {
		return base, nil
	}
	var metadata struct {
		DefaultBranch string `json:"defaultBranch"`
	}
	if _, err := p.decode(ctx, *repo.Integration, rp, nil, "default branch", &metadata); err != nil {
		return "", err
	}
	if metadata.DefaultBranch == "" {
		return "", fmt.Errorf("%w: azuredevops: default branch", port.ErrNotFound)
	}
	from, err := p.commitOf(ctx, repo, strings.TrimPrefix(metadata.DefaultBranch, "refs/heads/"))
	if err != nil {
		return "", err
	}
	var comparison struct {
		CommonCommit string `json:"commonCommit"`
	}
	if _, err := p.decode(ctx, *repo.Integration, rp+"/diffs/commits", url.Values{
		"baseVersion": {from}, "baseVersionType": {"commit"}, "targetVersion": {head}, "targetVersionType": {"commit"},
		"diffCommonCommit": {"true"}, "$top": {"1"}, "$skip": {"0"},
	}, "push default merge base", &comparison); err != nil {
		return "", err
	}
	if !port.ValidCommit(comparison.CommonCommit) {
		return "", fmt.Errorf("%w: azuredevops: missing push merge base", kernel.ErrUnavailable)
	}
	return comparison.CommonCommit, nil
}

func (p *Provider) pushCommitFiles(ctx context.Context, in port.Integration, rp, sha string) ([]string, error) {
	paths := []string{}
	seen := map[string]bool{}
	total := -1
	for skip := 0; ; {
		var page struct {
			Changes      *[]change      `json:"changes"`
			ChangeCounts map[string]int `json:"changeCounts"`
		}
		if _, err := p.decode(
			ctx,
			in,
			rp+"/commits/"+sha+"/changes",
			url.Values{"top": {strconv.Itoa(top)}, "skip": {strconv.Itoa(skip)}},
			"push commit changes",
			&page,
		); err != nil {
			return nil, err
		}
		if page.Changes == nil || page.ChangeCounts == nil {
			return nil, fmt.Errorf("%w: azuredevops: incomplete push changes page", kernel.ErrUnavailable)
		}
		count := 0
		for _, value := range page.ChangeCounts {
			if value < 0 {
				return nil, fmt.Errorf("%w: azuredevops: invalid push change count", kernel.ErrUnavailable)
			}
			count += value
		}
		if total >= 0 && total != count || skip+len(*page.Changes) > count {
			return nil, fmt.Errorf("%w: azuredevops: inconsistent push change count", kernel.ErrUnavailable)
		}
		total = count
		for _, change := range *page.Changes {
			if change.Item.Path == "" || seen[change.Item.Path] {
				return nil, fmt.Errorf("%w: azuredevops: repeated or missing push change path", kernel.ErrUnavailable)
			}
			seen[change.Item.Path] = true
			if !change.Item.IsFolder {
				for _, path := range []string{change.Item.Path, change.SourceServerItem, change.OriginalPath} {
					if path != "" {
						paths = append(paths, strings.TrimPrefix(path, "/"))
					}
				}
			}
		}
		skip += len(*page.Changes)
		if skip == total {
			slices.Sort(paths)
			return slices.Compact(paths), nil
		}
		if len(*page.Changes) == 0 {
			return nil, fmt.Errorf("%w: azuredevops: truncated push changes", kernel.ErrUnavailable)
		}
	}
}
