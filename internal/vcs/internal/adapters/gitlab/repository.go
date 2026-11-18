package gitlab

import (
	"context"
	"fmt"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RepositorySource = (*Provider)(nil)

// ResolveRepositoryRef retains the project ID across namespace and path edits.
func (p *Provider) ResolveRepositoryRef(ctx context.Context, repo port.Repository, ref string) (port.Resolution, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	commit, err := p.ResolveRef(ctx, repo, ref)
	if err != nil {
		return port.Resolution{}, err
	}
	c, _, pid, err := p.repo(repo)
	if err != nil {
		return port.Resolution{}, err
	}
	r, resp, err := c.Projects.GetProject(pid, nil, gitlab.WithContext(ctx))
	if err != nil {
		return port.Resolution{}, classify(resp, err, "repository identity")
	}
	namespace := strings.TrimSuffix(r.PathWithNamespace, "/"+r.Path)
	if r.ID <= 0 || namespace == r.PathWithNamespace || namespace == "" || r.Path == "" {
		return port.Resolution{}, fmt.Errorf("%w: gitlab: missing repository identity", kernel.ErrUnavailable)
	}
	return port.Resolution{Commit: commit, Repository: vcsapi.Repository{ID: fmt.Sprint(r.ID), Namespace: namespace, Name: r.Path}}, nil
}
