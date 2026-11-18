package github_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitDeploymentReport(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := port.DeploymentStack{ID: kernel.NewULID(), Slug: "infra", Environment: "infraweave/infra"}
	run := port.DeploymentRun{ID: kernel.NewULID(), Commit: l.repo.Head, Ref: "main"}
	if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentPending); err != nil {
		t.Fatalf("report pending deployment: %v", err)
	}
	for _, state := range []port.DeploymentState{port.DeploymentQueued, port.DeploymentInProgress, port.DeploymentSuccess, port.DeploymentSuccess} {
		if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, state); err != nil {
			t.Fatal(err)
		}
	}
	got := l.fake.Deployments("acme", "infra")
	if len(got) != 1 {
		t.Fatalf("deployments %+v", got)
	}
	d := got[0]
	var payload struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(d.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	states := make([]string, 0, len(d.Statuses))
	for _, s := range d.Statuses {
		states = append(states, s.State)
	}
	if d.Ref != run.Commit || d.SHA != run.Commit || d.Task != "infraweave apply" || d.Environment != stack.Environment || d.AutoMerge == nil || *d.AutoMerge ||
		d.ProductionEnvironment == nil || !*d.ProductionEnvironment || d.RequiredContexts == nil || len(d.RequiredContexts) != 0 || payload.RunID != run.ID.String() ||
		!slices.Equal(states, []string{"pending", "queued", "in_progress", "success"}) {
		t.Fatalf("native deployment %+v payload %+v states %v", d, payload, states)
	}
	run.ID = kernel.NewULID()
	if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentCanceled); err != nil {
		t.Fatal(err)
	}
	got = l.fake.Deployments("acme", "infra")
	if len(got) != 2 || !slices.Equal([]string{got[1].Statuses[0].State, got[1].Statuses[1].State}, []string{"pending", "failure"}) {
		t.Fatalf("per-run identity/cancellation %+v", got)
	}
}

func TestUnitDeploymentLostCreate(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := port.DeploymentStack{ID: kernel.NewULID(), Slug: "infra", Environment: "production"}
	run := port.DeploymentRun{ID: kernel.NewULID(), Commit: l.repo.Head, Ref: "main"}
	l.fake.FailNextDeploymentCreate()
	if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentInProgress); err == nil {
		t.Fatal("lost response succeeded")
	}
	if len(l.fake.Deployments("acme", "infra")) != 1 {
		t.Fatal("create not persisted before response loss")
	}
	if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentInProgress); err != nil {
		t.Fatal(err)
	}
	got := l.fake.Deployments("acme", "infra")
	if len(got) != 1 || len(got[0].Statuses) != 2 || got[0].Statuses[0].State != "pending" || got[0].Statuses[1].State != "in_progress" {
		t.Fatalf("recovered deployment %+v", got)
	}
}

func TestUnitDeploymentRefusals(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := port.DeploymentStack{ID: kernel.NewULID(), Environment: "production"}
	run := port.DeploymentRun{ID: kernel.NewULID(), Commit: l.repo.Head}
	before := l.fake.Requests()
	if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, "unsupported"); !errors.Is(err, port.ErrDeploymentInput) || l.fake.Requests() != before {
		t.Fatalf("invalid state=%v", err)
	}
	l.fake.FailPath("/repos/acme/infra/deployments", githubfake.RateLimited, time.Minute)
	if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentPending); err == nil {
		t.Fatal("rate limit ignored")
	} else if _, ok := errors.AsType[*port.RateLimited](err); !ok {
		t.Fatalf("rate limit type %v", err)
	}
	l.fake.Fail(githubfake.None, 0)
	l.fake.SetApp(l.app.ID, map[string]string{"contents": "read", "deployments": "read"}, nil)
	if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentPending); err == nil {
		t.Fatal("read-only deployments token wrote")
	}
	if len(l.fake.Deployments("acme", "infra")) != 0 {
		t.Fatal("refused write changed deployments")
	}
}

func TestUnitDeploymentPagination(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := port.DeploymentStack{ID: kernel.NewULID(), Environment: "production"}
	first := port.DeploymentRun{ID: kernel.NewULID(), Commit: l.repo.Head, Ref: "main"}
	if err := l.gh.Report(t.Context(), l.repo.Repo, stack, first, port.DeploymentPending); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		run := first
		run.ID = kernel.NewULID()
		if err := l.gh.Report(t.Context(), l.repo.Repo, stack, run, port.DeploymentPending); err != nil {
			t.Fatal(err)
		}
	}
	// The matching run is on the second native page, despite sharing SHA
	// and task with the first hundred records.
	for i := range 101 {
		state := port.DeploymentSuccess
		if i%2 == 0 {
			state = port.DeploymentFailure
		}
		if err := l.gh.Report(t.Context(), l.repo.Repo, stack, first, state); err != nil {
			t.Fatal(err)
		}
	}
	got := l.fake.Deployments("acme", "infra")
	if len(got) != 101 || len(got[0].Statuses) != 102 || got[0].Statuses[101].State != "failure" {
		t.Fatalf("paginated recovery/states: %d deployments, first %+v", len(got), got[0])
	}
}
