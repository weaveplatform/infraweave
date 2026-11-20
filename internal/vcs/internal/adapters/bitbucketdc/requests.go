package bitbucketdc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RequestReader = (*Provider)(nil)

// ReadRequest reads both native peers and their immutable latest commits.
// ref: https://developer.atlassian.com/server/bitbucket/rest/v902/api-group-pull-requests/
func (p *Provider) ReadRequest(ctx context.Context, repo port.Repository, number int) (port.RequestSnapshot, error) {
	if number < 1 {
		return port.RequestSnapshot{}, port.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return port.RequestSnapshot{}, err
	}
	var r webhookRequest
	if err := p.decode(ctx, *repo.Integration, rp+"/pull-requests/"+strconv.Itoa(number), nil, "native pull request", &r); err != nil {
		return port.RequestSnapshot{}, err
	}
	event, err := dcRequest(webhookPayload{PullRequest: &r}, "pr:modified")
	if err != nil || r.ID != number {
		return port.RequestSnapshot{}, fmt.Errorf("%w: bitbucketdc: invalid native pull request", kernel.ErrUnavailable)
	}
	target, _ := dcRepository(r.ToRef.Repository)
	source, _ := dcRepository(r.FromRef.Repository)
	out := port.RequestSnapshot{Request: *event.PullRequest, Head: r.FromRef.LatestCommit, Base: r.ToRef.LatestCommit, BaseRepository: target, HeadRepository: source}
	out.Request.HeadRepository = &out.HeadRepository
	if r.Reviewers != nil || r.Participants != nil {
		out.Approved = new(event.PullRequest.Approved)
	}
	return out, nil
}
