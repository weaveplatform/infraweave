package github

import (
	"context"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RepositorySource = (*Provider)(nil)

// ResolveRepositoryRef retains GitHub's numeric identity and stable owner ID.
func (p *Provider) ResolveRepositoryRef(ctx context.Context, repo port.Repository, ref string) (port.Resolution, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	commit, err := p.ResolveRef(ctx, repo, ref)
	if err != nil {
		return port.Resolution{}, err
	}
	c, _, err := p.repo(ctx, repo)
	if err != nil {
		return port.Resolution{}, err
	}
	r, _, err := c.Repositories.Get(ctx, repo.Namespace, repo.Name)
	if err != nil {
		return port.Resolution{}, classify(err, "repository identity")
	}
	if r.GetID() <= 0 || r.GetName() == "" || r.GetOwner().GetLogin() == "" {
		return port.Resolution{}, fmt.Errorf("%w: github: missing repository identity", kernel.ErrUnavailable)
	}
	identity := vcsapi.Repository{ID: strconv.FormatInt(r.GetID(), 10), Namespace: r.GetOwner().GetLogin(), Name: r.GetName()}
	if r.GetOwner().GetID() > 0 {
		identity.OwnerID = strconv.FormatInt(r.GetOwner().GetID(), 10)
	}
	return port.Resolution{Commit: commit, Repository: identity}, nil
}
