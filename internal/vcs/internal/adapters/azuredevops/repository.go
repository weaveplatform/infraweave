package azuredevops

import (
	"context"
	"fmt"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RepositorySource = (*Provider)(nil)

// ResolveRepositoryRef retains the repository UUID and current project name.
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
		ID      string `json:"id"`
		Name    string `json:"name"`
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
	}
	if _, err := p.decode(ctx, *repo.Integration, rp, url.Values{}, "repository identity", &native); err != nil {
		return port.Resolution{}, err
	}
	if native.ID == "" || native.Name == "" || native.Project.Name == "" {
		return port.Resolution{}, fmt.Errorf("%w: azuredevops: missing repository identity", kernel.ErrUnavailable)
	}
	identity := vcsapi.Repository{ID: native.ID, Namespace: native.Project.Name, Name: native.Name}
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
