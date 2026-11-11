package github_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
)

// TestAccGitHubLab runs the source contract against the vcs-lab
// job's repository on github.com, through the lab App, and checks that a
// run's token reads that repository and not another the App's
// installation reaches. GitHub revokes nothing: the token expires.
func TestAccGitHubLab(t *testing.T) {
	lab := sourcetest.LabEnv(t, vcslab.EnvUser, vcslab.EnvToken, vcslab.EnvNamespace,
		vcslab.EnvRepository, vcslab.EnvOther)
	in := port.Integration{
		Provider: "GITHUB", Settings: port.Settings{AppID: lab.User}, Credential: []byte(lab.Token),
	}
	gh := github.New(github.Options{})
	repo := port.Repository{Namespace: lab.Namespace, Name: lab.Repository, Integration: &in}
	gone := repo
	gone.Name += "-gone"
	sourcetest.Run(t, gh, sourcetest.Lab(repo, gone))

	c, err := gh.Mint(t.Context(), repo, "infraweave-run-lab", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	clone := func(name string) string {
		return "https://github.com/" + lab.Namespace + "/" + name + ".git"
	}
	sourcetest.Scope(t, http.DefaultClient, c, clone(lab.Repository), clone(lab.Other), false)
}
