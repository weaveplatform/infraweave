package bitbucketdc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RepositorySource = (*Provider)(nil)

// ResolveRepositoryRef retains the numeric repository ID across slug changes.
func (p *Provider) ResolveRepositoryRef(ctx context.Context, repo port.Repository, ref string) (port.Resolution, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var commit vcsapi.Commit
	var err error
	if repo.ExpectedID == "" {
		commit, err = p.resolve(ctx, repo, ref)
		if err != nil {
			return port.Resolution{}, err
		}
	}
	rp, err := repoPath(repo)
	if err != nil {
		return port.Resolution{}, err
	}
	var native struct {
		ID      int64  `json:"id"`
		Slug    string `json:"slug"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	}
	if err := p.decode(ctx, *repo.Integration, rp, nil, "repository identity", &native); err != nil {
		return port.Resolution{}, err
	}
	if native.ID <= 0 || native.Slug == "" || native.Project.Key == "" {
		return port.Resolution{}, fmt.Errorf("%w: bitbucketdc: missing repository identity", kernel.ErrUnavailable)
	}
	identity := vcsapi.Repository{ID: strconv.FormatInt(native.ID, 10), Namespace: native.Project.Key, Name: native.Slug}
	if err := port.MatchRepository(repo, identity); err != nil {
		return port.Resolution{}, err //nolint:wrapcheck // a fixed private identity refusal
	}
	if repo.ExpectedID != "" {
		repo.Namespace, repo.Name = identity.Namespace, identity.Name
		commit, err = p.resolve(ctx, repo, ref)
		if err != nil {
			return port.Resolution{}, err
		}
	}
	return port.Resolution{Commit: commit, Repository: identity}, nil
}
