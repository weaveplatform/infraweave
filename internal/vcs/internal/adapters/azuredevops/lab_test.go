package azuredevops_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
)

// TestAccAzureDevOpsLab runs the source contract against the vcs-lab
// job's repository on dev.azure.com, and checks that the token a run
// borrows reads it and every other repository the integration reaches,
// as Azure DevOps mints no narrower one.
func TestAccAzureDevOpsLab(t *testing.T) {
	lab := sourcetest.LabEnv(t, vcslab.EnvHost, vcslab.EnvToken, vcslab.EnvNamespace,
		vcslab.EnvRepository, vcslab.EnvOther)
	in := integrationOf(lab.Host, lab.Token, lab.Namespace)
	ado := azuredevops.New(azuredevops.Options{})
	repo := port.Repository{Namespace: lab.Namespace, Name: lab.Repository, Integration: &in}
	gone := repo
	gone.Name += "-gone"
	sourcetest.Run(t, ado, sourcetest.Lab(repo, gone))

	clone := func(name string) string {
		return strings.TrimSuffix(lab.Host, "/") + "/" + lab.Namespace + "/_git/" + name
	}
	sourcetest.Scope(
		t,
		http.DefaultClient,
		ado.Lend(in),
		clone(lab.Repository),
		clone(lab.Other),
		true,
	)
}
