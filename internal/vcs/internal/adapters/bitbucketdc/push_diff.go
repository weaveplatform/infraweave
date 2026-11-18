package bitbucketdc

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func (p *Provider) pushAffectedFiles(ctx context.Context, repo port.Repository, base, head string) ([]string, error) {
	rp, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	if port.ZeroCommit(base) {
		var branch ref
		if err := p.decode(ctx, *repo.Integration, rp+"/branches/default", nil, "default branch", &branch); err != nil {
			return nil, err
		}
		if branch.LatestCommit == "" {
			return nil, fmt.Errorf("%w: bitbucketdc: missing default branch head", kernel.ErrUnavailable)
		}
		var ancestor commit
		if err := p.decode(
			ctx,
			*repo.Integration,
			rp+"/commits/"+url.PathEscape(head)+"/merge-base",
			url.Values{"otherCommitId": {branch.LatestCommit}},
			"default branch merge base",
			&ancestor,
		); err != nil {
			return nil, err
		}
		if ancestor.ID == "" {
			return nil, fmt.Errorf("%w: bitbucketdc: missing merge base", kernel.ErrUnavailable)
		}
		base = ancestor.ID
	}
	// The global /changes response has an unpageable hard cap. Enumerate both
	// complete trees, then ask for one file at a time. Truncated hunk content
	// still proves that file changed; it cannot hide other candidate paths.
	trees := make([]map[string]bool, 0, 2)
	for _, ref := range []string{base, head} {
		paths, err := all[string](ctx, p, *repo.Integration, rp+"/files", url.Values{"at": {ref}}, "push tree files")
		if err != nil {
			return nil, err
		}
		tree := make(map[string]bool, len(paths))
		for _, path := range paths {
			tree[path] = true
		}
		trees = append(trees, tree)
	}
	paths := make(map[string]bool, len(trees[0])+len(trees[1]))
	for _, tree := range trees {
		for name := range tree {
			paths[name] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	changed := make([]string, 0, len(ordered))
	for _, name := range ordered {
		if !trees[0][name] || !trees[1][name] {
			changed = append(changed, name)
			continue
		}
		differs, err := p.pushFileChanged(ctx, *repo.Integration, rp, name, base, head)
		if err != nil {
			return nil, err
		}
		if differs {
			changed = append(changed, name)
		}
	}
	return changed, nil
}

type diffPath struct {
	String string `json:"toString"`
}

func (p *Provider) pushFileChanged(ctx context.Context, in port.Integration, rp, name, base, head string) (bool, error) {
	var diff struct {
		Diffs []struct {
			Source      *diffPath `json:"source"`
			Destination *diffPath `json:"destination"`
		} `json:"diffs"`
		Truncated bool `json:"truncated"`
	}
	if err := p.decode(ctx, in, rp+"/diff/"+escape(name), url.Values{
		"since": {base}, "until": {head}, "contextLines": {"0"},
	}, "push file diff", &diff); err != nil {
		return false, err
	}
	if diff.Diffs == nil {
		return false, fmt.Errorf("%w: bitbucketdc: file diff has no result", kernel.ErrUnavailable)
	}
	if len(diff.Diffs) == 0 {
		if diff.Truncated {
			return false, fmt.Errorf("%w: bitbucketdc: file diff truncated without identity", kernel.ErrUnavailable)
		}
		return false, nil
	}
	for _, file := range diff.Diffs {
		if file.Source != nil && file.Source.String == name || file.Destination != nil && file.Destination.String == name {
			return true, nil
		}
	}
	return false, fmt.Errorf("%w: bitbucketdc: file diff has no requested path", kernel.ErrUnavailable)
}
