package github

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RequestReader = (*Provider)(nil)

// ReadRequest retains the native source and target commits, including forks.
// Aggregate approval and ancestry remain absent: a review or clean merge preview
// alone does not prove either condition.
// ref: https://docs.github.com/en/rest/pulls/pulls#get-a-pull-request
func (p *Provider) ReadRequest(ctx context.Context, repo port.Repository, number int) (port.RequestSnapshot, error) {
	if number < 1 {
		return port.RequestSnapshot{}, port.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, err := p.repo(ctx, repo)
	if err != nil {
		return port.RequestSnapshot{}, err
	}
	r, _, err := c.PullRequests.Get(ctx, repo.Namespace, repo.Name, number)
	if err != nil {
		return port.RequestSnapshot{}, classify(err, "native pull request")
	}
	event, err := (&webhook{PullRequest: r}).pullRequest(vcsapi.Event{Type: "pull_request", Action: "edited"})
	if err != nil || r.GetNumber() != number {
		return port.RequestSnapshot{}, fmt.Errorf("%w: github: invalid native pull request", kernel.ErrUnavailable)
	}
	out := port.RequestSnapshot{
		Request: *event.PullRequest, Base: r.GetBase().GetSHA(), Head: r.GetHead().GetSHA(),
		BaseRepository: nativeRequestRepository(r.GetBase().GetRepo()), HeadRepository: nativeRequestRepository(r.GetHead().GetRepo()),
	}
	out.Request.HeadRepository = &out.HeadRepository
	if r.Mergeable != nil && r.MergeableState != nil {
		out.Mergeable = new(r.GetMergeable() && r.GetMergeableState() == "clean")
	}
	return out, nil
}

func nativeRequestRepository(r *github.Repository) vcsapi.Repository {
	if r.GetID() <= 0 {
		return vcsapi.Repository{}
	}
	out := vcsapi.Repository{ID: strconv.FormatInt(r.GetID(), 10), Namespace: r.GetOwner().GetLogin(), Name: r.GetName()}
	if r.GetOwner().GetID() > 0 {
		out.OwnerID = strconv.FormatInt(r.GetOwner().GetID(), 10)
	}
	return out
}
