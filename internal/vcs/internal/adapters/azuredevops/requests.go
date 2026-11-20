package azuredevops

import (
	"context"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RequestReader = (*Provider)(nil)

// ReadRequest retains the native preview's immutable source and target commits.
// ref: https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-requests/get-pull-request?view=azure-devops-rest-7.1
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
	if _, err := p.decode(ctx, *repo.Integration, rp+"/pullRequests/"+strconv.Itoa(number), nil, "native pull request", &r); err != nil {
		return port.RequestSnapshot{}, err
	}
	request, valid := r.request()
	target, validTarget := r.Repository.repository()
	if !valid || !validTarget || r.PullRequestID != number {
		return port.RequestSnapshot{}, fmt.Errorf("%w: azuredevops: invalid native pull request", kernel.ErrUnavailable)
	}
	source := target
	if r.ForkSource != nil {
		var validSource bool
		source, validSource = r.ForkSource.Repository.repository()
		if !validSource {
			return port.RequestSnapshot{}, fmt.Errorf("%w: azuredevops: missing native fork identity", kernel.ErrUnavailable)
		}
	}
	out := port.RequestSnapshot{Request: request, Base: r.LastMergeTargetCommit.CommitID, Head: r.LastMergeSourceCommit.CommitID, BaseRepository: target, HeadRepository: source}
	out.Request.HeadRepository = &out.HeadRepository
	if r.Reviewers != nil {
		out.Approved = new(request.Approved)
	}
	return out, nil
}
