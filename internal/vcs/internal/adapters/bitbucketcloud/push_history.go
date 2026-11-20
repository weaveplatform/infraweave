package bitbucketcloud

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.PushHistory = (*Provider)(nil)

// RecoverPush follows native include/exclude pagination and each commit's
// paged first-parent diffstat, independently of the webhook's five-commit cap.
func (p *Provider) RecoverPush(ctx context.Context, repo port.Repository, base, head string) (result port.PushRange, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer func() {
		// Native I/O can report EOF after cancellation; preserve both causes.
		if retErr != nil {
			retErr = errors.Join(retErr, ctx.Err())
		}
		cancel()
	}()
	rp, err := repoPath(repo)
	if err != nil {
		return port.PushRange{}, err
	}
	base, err = p.pushBase(ctx, repo, rp, base, head)
	if err != nil {
		return port.PushRange{}, err
	}
	commits, err := all[commit](ctx, p, *repo.Integration, rp+"/commits", url.Values{"include": {head}, "exclude": {base}}, "push range commits")
	if err != nil {
		return port.PushRange{}, err
	}
	items := make([]vcsapi.PushCommit, 0, len(commits))
	for _, commit := range commits {
		files, err := p.pushCommitFiles(ctx, *repo.Integration, rp, commit.Hash)
		if err != nil {
			return port.PushRange{}, err
		}
		items = append(items, port.RecoveredCommit(p.out(repo, commit), files))
	}
	facts, err := port.RecoveredRange(base, "bitbucketcloud/REST include-exclude commits and paged diffstat", items)
	if err != nil {
		return port.PushRange{}, fmt.Errorf("bitbucketcloud: recover push range: %w", err)
	}
	return facts, nil
}

func (p *Provider) pushBase(ctx context.Context, repo port.Repository, rp, base, head string) (string, error) {
	if !port.ZeroCommit(base) {
		return base, nil
	}
	var metadata struct {
		MainBranch struct {
			Name string `json:"name"`
		} `json:"mainbranch"`
	}
	if err := p.decode(ctx, *repo.Integration, p.api+rp, "default branch", &metadata); err != nil {
		return "", err
	}
	if metadata.MainBranch.Name == "" {
		return "", fmt.Errorf("%w: bitbucketcloud: default branch", port.ErrNotFound)
	}
	var ancestor commit
	if err := p.decode(
		ctx,
		*repo.Integration,
		p.api+rp+"/merge-base/"+url.PathEscape(head)+".."+url.PathEscape(metadata.MainBranch.Name),
		"push merge base",
		&ancestor,
	); err != nil {
		return "", err
	}
	if !port.ValidCommit(ancestor.Hash) {
		return "", fmt.Errorf("%w: bitbucketcloud: missing push merge base", kernel.ErrUnavailable)
	}
	return ancestor.Hash, nil
}

type pushDiffSide struct {
	Path string `json:"path"`
}

func (p *Provider) pushCommitFiles(ctx context.Context, in port.Integration, rp, sha string) ([]string, error) {
	if !port.ValidCommit(sha) {
		return nil, fmt.Errorf("%w: bitbucketcloud: invalid push commit", kernel.ErrUnavailable)
	}
	stats, err := all[struct {
		Old *pushDiffSide `json:"old"`
		New *pushDiffSide `json:"new"`
	}](ctx, p, in, rp+"/diffstat/"+url.PathEscape(sha), url.Values{}, "push commit diffstat")
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, stat := range stats {
		if stat.Old == nil && stat.New == nil {
			return nil, fmt.Errorf("%w: bitbucketcloud: incomplete push commit diffstat", kernel.ErrUnavailable)
		}
		for _, side := range []*pushDiffSide{stat.Old, stat.New} {
			if side != nil {
				if side.Path == "" {
					return nil, fmt.Errorf("%w: bitbucketcloud: missing push commit path", kernel.ErrUnavailable)
				}
				paths = append(paths, side.Path)
			}
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}
