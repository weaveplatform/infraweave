package port

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// DeploymentState is the provider-independent state of an infrastructure
// deployment. Providers map cancellation to their available failure states.
type DeploymentState string

const (
	DeploymentPending    DeploymentState = "pending"
	DeploymentQueued     DeploymentState = "queued"
	DeploymentInProgress DeploymentState = "in_progress"
	DeploymentSuccess    DeploymentState = "success"
	DeploymentFailure    DeploymentState = "failure"
	DeploymentCanceled   DeploymentState = "canceled"
)

// DeploymentStack identifies the stack and its selected deployment environment.
// The VCS domain resolves label overrides before enqueueing a report.
type DeploymentStack struct {
	ID          kernel.ULID
	Slug        string
	Environment string
}

// DeploymentRun identifies the tracked run and its immutable repository commit.
// GitHub retains ID in its payload; GitLab identifies its native deployment by
// environment, Ref and Commit. Tag distinguishes a tag from a branch ref.
type DeploymentRun struct {
	ID     kernel.ULID
	Commit string
	Ref    string
	Tag    bool
}

// Deployments reports an infrastructure deployment through a provider's API.
// A retry recovers an existing deployment before creating one. Only queue job
// handlers call Report; target selection and event consumption never do.
type Deployments interface {
	Report(ctx context.Context, repo Repository, stack DeploymentStack, run DeploymentRun, state DeploymentState) error
}
