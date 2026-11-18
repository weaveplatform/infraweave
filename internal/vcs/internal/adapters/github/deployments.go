package github

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/go-github/v93/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

const deploymentTask = "infraweave apply"

// ErrDeployment is an incomplete success response from the deployments API.
var ErrDeployment = errors.New("github: invalid deployment response")

type deploymentPayload struct {
	RunID string `json:"runId"`
}

// Report recovers the run's deployment before creating it, then updates its
// status. The entire call, including pagination and App authentication, is
// bounded by Timeout and performs no local retry.
func (p *Provider) Report(ctx context.Context, repo port.Repository, stack port.DeploymentStack, run port.DeploymentRun, state port.DeploymentState) error {
	wanted := string(state)
	if state == port.DeploymentCanceled {
		wanted = string(port.DeploymentFailure)
	}
	switch wanted {
	case "pending", "queued", "in_progress", "success", "failure":
	default:
		return port.ErrDeploymentInput
	}
	if stack.ID.IsZero() || stack.Environment == "" || run.ID.IsZero() || run.Commit == "" {
		return port.ErrDeploymentInput
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	a, installation, err := p.installationOf(ctx, repo)
	if err != nil {
		return err
	}
	c := p.installationFor(a, installation, DeploymentWrite)
	d, err := findDeployment(ctx, c, repo, run)
	if err != nil {
		return err
	}
	if d == nil {
		d, _, err = c.Repositories.CreateDeployment(ctx, repo.Namespace, repo.Name, github.DeploymentRequest{
			Ref: run.Commit, Task: new(deploymentTask), AutoMerge: new(false),
			RequiredContexts: []string{}, Payload: deploymentPayload{RunID: run.ID.String()},
			Environment: new(stack.Environment), ProductionEnvironment: new(true),
		})
		if err != nil {
			return classify(err, "create deployment")
		}
		if d == nil || d.GetID() <= 0 {
			return ErrDeployment
		}
		if err := postDeploymentState(ctx, c, repo, d.GetID(), "pending"); err != nil {
			return err
		}
		if wanted == "pending" {
			return nil
		}
	}
	current, err := deploymentStatus(ctx, c, repo, d.GetID())
	if err != nil || current == wanted {
		return err
	}
	if current == "" {
		// A create can reach GitHub even when its response is lost. Recovery
		// still establishes the initial status before the requested later one.
		if err := postDeploymentState(ctx, c, repo, d.GetID(), "pending"); err != nil {
			return err
		}
		if wanted == "pending" {
			return nil
		}
	}
	return postDeploymentState(ctx, c, repo, d.GetID(), wanted)
}

func findDeployment(ctx context.Context, c *github.Client, repo port.Repository, run port.DeploymentRun) (*github.Deployment, error) {
	opts := &github.DeploymentsListOptions{SHA: run.Commit, Task: deploymentTask, ListOptions: github.ListOptions{PerPage: 100}}
	for {
		list, resp, err := c.Repositories.ListDeployments(ctx, repo.Namespace, repo.Name, opts)
		if err != nil {
			return nil, classify(err, "list deployments")
		}
		for _, candidate := range list {
			var payload deploymentPayload
			if candidate != nil && candidate.GetID() > 0 && candidate.GetSHA() == run.Commit &&
				candidate.GetTask() == deploymentTask && json.Unmarshal(candidate.Payload, &payload) == nil && payload.RunID == run.ID.String() {
				return candidate, nil
			}
		}
		if resp.NextPage == 0 {
			return nil, nil
		}
		opts.Page = resp.NextPage
	}
}

func deploymentStatus(ctx context.Context, c *github.Client, repo port.Repository, id int64) (string, error) {
	opts := &github.ListOptions{PerPage: 100}
	var latest *github.DeploymentStatus
	for {
		list, resp, err := c.Repositories.ListDeploymentStatuses(ctx, repo.Namespace, repo.Name, id, opts)
		if err != nil {
			return "", classify(err, "list deployment statuses")
		}
		for _, status := range list {
			if status != nil && (latest == nil || status.GetID() > latest.GetID()) {
				latest = status
			}
		}
		if resp.NextPage == 0 {
			return latest.GetState(), nil
		}
		opts.Page = resp.NextPage
	}
}

func postDeploymentState(ctx context.Context, c *github.Client, repo port.Repository, id int64, state string) error {
	status, _, err := c.Repositories.CreateDeploymentStatus(ctx, repo.Namespace, repo.Name, id, github.DeploymentStatusRequest{State: state})
	if err != nil {
		return classify(err, "create deployment status")
	}
	if status == nil || status.GetID() <= 0 {
		return ErrDeployment
	}
	return nil
}

var _ port.Deployments = (*Provider)(nil)
