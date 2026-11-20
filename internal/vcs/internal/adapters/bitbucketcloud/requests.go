package bitbucketcloud

import (
	"context"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RequestReader = (*Provider)(nil)

// ReadRequest resolves the immutable native source and destination references.
// ref: https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-get
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
	if err := p.decode(ctx, *repo.Integration, p.api+rp+"/pullrequests/"+strconv.Itoa(number), "native pull request", &r); err != nil {
		return port.RequestSnapshot{}, err
	}
	target, _ := cloudRepository(r.Destination.Repository)
	event, err := cloudRequest(webhookPayload{PullRequest: &r}, "pullrequest:updated", target)
	if err != nil || r.ID != number {
		return port.RequestSnapshot{}, fmt.Errorf("%w: bitbucketcloud: invalid native pull request", kernel.ErrUnavailable)
	}
	source, _ := cloudRepository(r.Source.Repository)
	head, err := p.ResolveRequestHead(ctx, *repo.Integration, source, r.Source.Commit.Hash)
	if err != nil {
		return port.RequestSnapshot{}, err
	}
	base, err := p.ResolveRequestHead(ctx, *repo.Integration, target, r.Destination.Commit.Hash)
	if err != nil {
		return port.RequestSnapshot{}, err
	}
	out := port.RequestSnapshot{Request: *event.PullRequest, Head: head, Base: base, BaseRepository: target, HeadRepository: source}
	if r.Participants != nil {
		out.Approved = new(event.PullRequest.Approved)
	}
	return out, nil
}
