package bitbucketdc

import (
	"context"
	"fmt"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.PushHistory = (*Provider)(nil)

// RecoverPush pages since-exclusive/until-inclusive native commits and uses
// complete file trees and per-path diffs instead of the capped changes list.
func (p *Provider) RecoverPush(ctx context.Context, repo port.Repository, base, head string) (port.PushRange, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return port.PushRange{}, err
	}
	h, err := hostOf(repo.Integration.Settings)
	if err != nil {
		return port.PushRange{}, err
	}
	base, err = p.pushBase(ctx, *repo.Integration, rp, base, head)
	if err != nil {
		return port.PushRange{}, err
	}
	commits, err := all[commit](ctx, p, *repo.Integration, rp+"/commits", url.Values{"since": {base}, "until": {head}}, "push range commits")
	if err != nil {
		return port.PushRange{}, err
	}
	items := make([]vcsapi.PushCommit, 0, len(commits))
	for _, commit := range commits {
		if commit.Parents == nil || commit.AuthorTimestamp == 0 {
			return port.PushRange{}, fmt.Errorf("%w: bitbucketdc: incomplete push commit", kernel.ErrUnavailable)
		}
		var files []string
		if len(commit.Parents) == 0 {
			files, err = all[string](ctx, p, *repo.Integration, rp+"/files", url.Values{"at": {commit.ID}}, "root commit files")
		} else {
			files, err = p.pushAffectedFiles(ctx, repo, commit.Parents[0].ID, commit.ID)
		}
		if err != nil {
			return port.PushRange{}, err
		}
		items = append(items, port.RecoveredCommit(out(h, repo, commit), files))
	}
	return port.RecoveredRange(base, "bitbucketdc/REST since-until commits and complete file diffs", items)
}

func (p *Provider) pushBase(ctx context.Context, in port.Integration, rp, base, head string) (string, error) {
	if !port.ZeroCommit(base) {
		return base, nil
	}
	var branch ref
	if err := p.decode(ctx, in, rp+"/branches/default", nil, "default branch", &branch); err != nil {
		return "", err
	}
	if !port.ValidCommit(branch.LatestCommit) {
		return "", fmt.Errorf("%w: bitbucketdc: missing default branch head", kernel.ErrUnavailable)
	}
	var ancestor commit
	if err := p.decode(ctx, in, rp+"/commits/"+url.PathEscape(head)+"/merge-base",
		url.Values{"otherCommitId": {branch.LatestCommit}}, "default branch merge base", &ancestor); err != nil {
		return "", err
	}
	if !port.ValidCommit(ancestor.ID) {
		return "", fmt.Errorf("%w: bitbucketdc: missing merge base", kernel.ErrUnavailable)
	}
	return ancestor.ID, nil
}
