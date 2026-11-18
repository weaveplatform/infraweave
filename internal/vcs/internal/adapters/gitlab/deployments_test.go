package gitlab_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitDeploymentReport(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := port.DeploymentStack{ID: kernel.NewULID(), Slug: "infra", Environment: "infraweave/infra"}
	run := port.DeploymentRun{ID: kernel.NewULID(), Commit: l.repo.Head, Ref: "main"}
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentPending); err != nil {
		t.Fatalf("report pending deployment: %v", err)
	}
	for _, state := range []port.DeploymentState{port.DeploymentQueued, port.DeploymentInProgress, port.DeploymentSuccess, port.DeploymentSuccess} {
		if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, state); err != nil {
			t.Fatal(err)
		}
	}
	got := l.fake.Deployments("acme/platform", "infra")
	if len(got) != 1 || len(l.fake.Environments("acme/platform", "infra")) != 1 || got[0].Environment.Name != stack.Environment || got[0].SHA != run.Commit || got[0].Ref != "main" || got[0].Tag || !slices.Equal(got[0].States, []string{"running", "success"}) {
		t.Fatalf("native deployment %+v", got)
	}
	run.ID = kernel.NewULID()
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentInProgress); err != nil {
		t.Fatal(err)
	}
	got = l.fake.Deployments("acme/platform", "infra")
	if len(got) != 1 || !slices.Equal(got[0].States, []string{"running", "success", "running"}) {
		t.Fatalf("native identity must reuse env/ref/SHA across runs: %+v", got)
	}
	for _, state := range []port.DeploymentState{port.DeploymentFailure, port.DeploymentCanceled} {
		if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, state); err != nil {
			t.Fatal(err)
		}
	}
	got = l.fake.Deployments("acme/platform", "infra")
	if got[0].Status != "canceled" {
		t.Fatalf("cancellation %+v", got)
	}
	run.Tag = true
	run.Ref = "v1"
	run.Commit = l.repo.Tagged
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentSuccess); err != nil {
		t.Fatal(err)
	}
	got = l.fake.Deployments("acme/platform", "infra")
	if len(got) != 2 || !got[1].Tag || got[1].Ref != "v1" {
		t.Fatalf("native tag identity %+v", got)
	}
}

func TestUnitDeploymentLostCreate(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := port.DeploymentStack{ID: kernel.NewULID(), Environment: "production"}
	run := port.DeploymentRun{ID: kernel.NewULID(), Commit: l.repo.Head, Ref: "main"}
	l.fake.FailNextDeploymentCreate()
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentInProgress); err == nil {
		t.Fatal("lost response succeeded")
	}
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentSuccess); err != nil {
		t.Fatal(err)
	}
	got := l.fake.Deployments("acme/platform", "infra")
	if len(got) != 1 || len(l.fake.Environments("acme/platform", "infra")) != 1 || !slices.Equal(got[0].States, []string{"running", "success"}) {
		t.Fatalf("recovery %+v", got)
	}
}

func TestUnitDeploymentRefusals(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := port.DeploymentStack{ID: kernel.NewULID(), Environment: "production"}
	run := port.DeploymentRun{ID: kernel.NewULID(), Commit: l.repo.Head, Ref: "main"}
	before := l.fake.Requests()
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, "unsupported"); !errors.Is(err, port.ErrDeploymentInput) || l.fake.Requests() != before {
		t.Fatalf("invalid state=%v", err)
	}
	l.fake.FailPath("/projects/acme/platform/infra/environments", gitlabfake.RateLimited, time.Minute)
	// Fault paths use decoded URL.Path, so the namespace remains slash-separated.
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentPending); err == nil {
		t.Fatal("rate limit ignored")
	} else if _, ok := errors.AsType[*port.RateLimited](err); !ok {
		t.Fatalf("rate limit type %v", err)
	}
	l.fake.Fail(gitlabfake.None, 0)
	l.fake.AddMember("acme/platform", "infra", "infraweave", gitlabfake.Reporter)
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentPending); err == nil {
		t.Fatal("reporter wrote deployment")
	}
	if len(l.fake.Deployments("acme/platform", "infra")) != 0 {
		t.Fatal("refused write changed deployments")
	}
}
