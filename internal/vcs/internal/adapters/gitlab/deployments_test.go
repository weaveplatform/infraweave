package gitlab_test

import (
	"errors"
	"fmt"
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
	if len(got) != 1 || len(l.fake.Environments("acme/platform", "infra")) != 1 || got[0].Environment.Name != stack.Environment || got[0].SHA != run.Commit ||
		got[0].Ref != "main" ||
		got[0].Tag ||
		!slices.Equal(got[0].States, []string{"running", "success"}) {
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

func TestUnitDeploymentHost(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	for _, tc := range []struct{ input, want string }{
		{"", "https://gitlab.com/api/v4/"},
		{"https://GITLAB.example.com:443/", "https://gitlab.example.com/api/v4/"},
		{"https://gitlab.example.com/api/v4/", "https://gitlab.example.com/api/v4/"},
		{"https://gitlab.example.com/group/", "https://gitlab.example.com/group/api/v4/"},
		{"https://gitlab.example.com:8443/", "https://gitlab.example.com:8443/api/v4/"},
		{"http://gitlab.example.com:80/", "http://gitlab.example.com/api/v4/"},
		{"https://[::1]:443/", "https://[::1]/api/v4/"},
	} {
		got, err := l.gl.DeploymentHost(port.Settings{APIHost: tc.input})
		if err != nil || got != tc.want {
			t.Errorf("host(%q)=%q,%v want %q", tc.input, got, err, tc.want)
		}
	}
	if _, err := l.gl.DeploymentHost(port.Settings{APIHost: "mailto:invalid"}); err == nil {
		t.Fatal("invalid host accepted")
	}
}

func TestUnitDeploymentPagination(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	stack := port.DeploymentStack{ID: kernel.NewULID(), Environment: "production"}
	var last port.DeploymentRun
	for i := range 101 {
		ref := fmt.Sprintf("release-%d", i)
		sha := l.fake.Repo("acme/platform", "infra").Commit(ref, ref, map[string]string{"main.tf": ref})
		last = port.DeploymentRun{ID: kernel.NewULID(), Commit: sha, Ref: ref}
		if err := l.gl.Report(t.Context(), l.repo.Repo, stack, last, port.DeploymentPending); err != nil {
			t.Fatal(err)
		}
	}
	// GitLab defaults to ascending ID. The last environment/ref/SHA match
	// is on page two and cannot be found using invented ref/SHA filters.
	if err := l.gl.Report(t.Context(), l.repo.Repo, stack, last, port.DeploymentSuccess); err != nil {
		t.Fatal(err)
	}
	got := l.fake.Deployments("acme/platform", "infra")
	if len(got) != 101 || !slices.Equal(got[100].States, []string{"running", "success"}) || len(l.fake.Environments("acme/platform", "infra")) != 1 {
		t.Fatalf("paginated native identity: %d deployments, last %+v", len(got), got[100])
	}
}
