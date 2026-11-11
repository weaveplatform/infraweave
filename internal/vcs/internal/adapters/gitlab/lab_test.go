package gitlab_test

import (
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
)

// TestAccGitLabLab runs the source contract against the vcs-lab
// job's project on its GitLab container, and checks that a run's project access token
// reads that project and not another the integration reaches, and
// nothing once revoked.
func TestAccGitLabLab(t *testing.T) {
	lab := sourcetest.LabEnv(t, vcslab.EnvHost, vcslab.EnvToken, vcslab.EnvNamespace,
		vcslab.EnvRepository, vcslab.EnvOther, vcslab.EnvCAFile)
	roots, client := sourcetest.LabRoots(t, lab.CAFile)
	in := integrationOf(lab.Token, lab.Host)
	gl := gitlab.New(gitlab.Options{RootCAs: roots})
	repo := port.Repository{Namespace: lab.Namespace, Name: lab.Repository, Integration: &in}
	gone := repo
	gone.Name += "-gone"
	sourcetest.Run(t, gl, sourcetest.Lab(repo, gone))

	c, err := gl.Mint(t.Context(), repo, "infraweave-run-lab", time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	clone := func(name string) string {
		return strings.TrimSuffix(lab.Host, "/") + "/" + lab.Namespace + "/" + name + ".git"
	}
	sourcetest.Scope(t, client, c, clone(lab.Repository), clone(lab.Other), false)
	if err := gl.Revoke(t.Context(), repo, c.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := sourcetest.Reads(
		t.Context(),
		client,
		clone(lab.Repository),
		c,
	); ok ||
		err != nil {
		t.Errorf("a revoked token reads = %t, %v", ok, err)
	}
}
