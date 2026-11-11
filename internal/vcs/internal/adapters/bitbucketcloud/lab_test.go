package bitbucketcloud_test

import (
	"net/http"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
)

// TestAccBitbucketCloudLab runs the source contract against the
// vcs-lab job's repository on bitbucket.org, and checks that the
// token a run borrows reads it and every other repository the
// integration reaches, as Bitbucket Cloud mints no narrower one.
func TestAccBitbucketCloudLab(t *testing.T) {
	lab := sourcetest.LabEnv(t, vcslab.EnvUser, vcslab.EnvToken, vcslab.EnvNamespace,
		vcslab.EnvRepository, vcslab.EnvOther)
	in := integrationOf(lab.User, lab.Token)
	bb := bitbucketcloud.New(bitbucketcloud.Options{})
	repo := port.Repository{Namespace: lab.Namespace, Name: lab.Repository, Integration: &in}
	gone := repo
	gone.Name += "-gone"
	sourcetest.Run(t, bb, sourcetest.Lab(repo, gone))

	clone := func(name string) string {
		return "https://bitbucket.org/" + lab.Namespace + "/" + name + ".git"
	}
	sourcetest.Scope(
		t,
		http.DefaultClient,
		bb.Lend(in),
		clone(lab.Repository),
		clone(lab.Other),
		true,
	)
}
