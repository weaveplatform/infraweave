package bitbucketcloud

import (
	"context"
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RepositorySource = (*Provider)(nil)

// ResolveRepositoryRef retains the repository UUID independently of its slug.
func (p *Provider) ResolveRepositoryRef(ctx context.Context, repo port.Repository, ref string) (port.Resolution, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	commit, err := p.resolve(ctx, repo, ref)
	if err != nil {
		return port.Resolution{}, err
	}
	rp, err := repoPath(repo)
	if err != nil {
		return port.Resolution{}, err
	}
	var native struct {
		ID       string `json:"uuid"`
		FullName string `json:"full_name"`
	}
	if err := p.decode(ctx, *repo.Integration, p.api+rp, "repository identity", &native); err != nil {
		return port.Resolution{}, err
	}
	namespace, name, ok := strings.Cut(native.FullName, "/")
	if native.ID == "" || !ok || namespace == "" || name == "" {
		return port.Resolution{}, fmt.Errorf("%w: bitbucketcloud: missing repository identity", kernel.ErrUnavailable)
	}
	return port.Resolution{Commit: commit, Repository: vcsapi.Repository{ID: native.ID, Namespace: namespace, Name: name}}, nil
}
