package gitlab

import (
	"context"
	"errors"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// ErrDeployment is an incomplete successful native deployment response.
var ErrDeployment = errors.New("gitlab: invalid deployment response")

// Report finds the named environment and its native ref/SHA deployment before
// creating either. GitLab exposes no per-run deployment payload; VCS's shared
// resource ordering and cursor fence serialize runs reusing this identity.
func (p *Provider) Report(ctx context.Context, repo port.Repository, stack port.DeploymentStack, run port.DeploymentRun, state port.DeploymentState) error {
	status, valid := nativeDeploymentState(state)
	if !valid || stack.ID.IsZero() || stack.Environment == "" || run.ID.IsZero() || run.Commit == "" || run.Ref == "" {
		return port.ErrDeploymentInput
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, project, err := p.repo(repo)
	if err != nil {
		return err
	}
	if err := ensureEnvironment(ctx, c, project, stack.Environment); err != nil {
		return err
	}
	d, err := findDeployment(ctx, c, project, stack.Environment, run)
	if err != nil {
		return err
	}
	if d == nil {
		created, resp, err := c.Deployments.CreateProjectDeployment(project, &gitlab.CreateProjectDeploymentOptions{
			Environment: new(stack.Environment), SHA: new(run.Commit), Ref: new(run.Ref), Tag: new(run.Tag), Status: new(status),
		}, gitlab.WithContext(ctx))
		if err != nil {
			return classify(resp, err, "create deployment")
		}
		if created == nil || created.ID <= 0 {
			return ErrDeployment
		}
		return nil
	}
	if d.Status == string(status) {
		return nil
	}
	updated, resp, err := c.Deployments.UpdateProjectDeployment(project, d.ID, &gitlab.UpdateProjectDeploymentOptions{Status: new(status)}, gitlab.WithContext(ctx))
	if err != nil {
		return classify(resp, err, "update deployment")
	}
	if updated == nil || updated.ID <= 0 {
		return ErrDeployment
	}
	return nil
}

func nativeDeploymentState(state port.DeploymentState) (gitlab.DeploymentStatusValue, bool) {
	switch state {
	case port.DeploymentPending, port.DeploymentQueued, port.DeploymentInProgress:
		return gitlab.DeploymentStatusValue("running"), true
	case port.DeploymentSuccess:
		return gitlab.DeploymentStatusValue("success"), true
	case port.DeploymentFailure:
		return gitlab.DeploymentStatusValue("failed"), true
	case port.DeploymentCanceled:
		return gitlab.DeploymentStatusValue("canceled"), true
	}
	return "", false
}

func ensureEnvironment(ctx context.Context, c *gitlab.Client, project, name string) error {
	found, err := findEnvironment(ctx, c, project, name)
	if err != nil || found {
		return err
	}
	_, resp, err := c.Environments.CreateEnvironment(project, &gitlab.CreateEnvironmentOptions{Name: new(name)}, gitlab.WithContext(ctx))
	if err == nil {
		return nil
	}
	if resp != nil && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusConflict) {
		// Another stack may have created the shared environment after the
		// read. Only finding that exact name makes a failed create benign.
		if found, lookupErr := findEnvironment(ctx, c, project, name); lookupErr != nil || found {
			return lookupErr
		}
	}
	return classify(resp, err, "create deployment environment")
}

func findEnvironment(ctx context.Context, c *gitlab.Client, project, name string) (bool, error) {
	opts := &gitlab.ListEnvironmentsOptions{Name: new(name), ListOptions: gitlab.ListOptions{PerPage: 100}}
	for {
		list, resp, err := c.Environments.ListEnvironments(project, opts, gitlab.WithContext(ctx))
		if err != nil {
			return false, classify(resp, err, "list deployment environments")
		}
		for _, environment := range list {
			if environment != nil && environment.ID > 0 && environment.Name == name {
				return true, nil
			}
		}
		if resp.NextPage == 0 {
			return false, nil
		}
		opts.Page = resp.NextPage
	}
}

func findDeployment(ctx context.Context, c *gitlab.Client, project, environment string, run port.DeploymentRun) (*gitlab.Deployment, error) {
	// The native list has no ref or SHA query filters. Paginate its
	// environment-filtered results and compare both documented fields.
	opts := &gitlab.ListProjectDeploymentsOptions{Environment: new(environment), ListOptions: gitlab.ListOptions{PerPage: 100}}
	for {
		list, resp, err := c.Deployments.ListProjectDeployments(project, opts, gitlab.WithContext(ctx))
		if err != nil {
			return nil, classify(resp, err, "list deployments")
		}
		for _, candidate := range list {
			if candidate != nil && candidate.ID > 0 && candidate.Environment != nil &&
				candidate.Environment.Name == environment && candidate.Ref == run.Ref && candidate.SHA == run.Commit {
				return candidate, nil
			}
		}
		if resp.NextPage == 0 {
			return nil, nil
		}
		opts.Page = resp.NextPage
	}
}

var _ port.Deployments = (*Provider)(nil)
