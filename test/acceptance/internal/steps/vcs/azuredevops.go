package vcs

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// azureDevOpsSteps run the Azure DevOps an @azure-devops scenario's
// servers reach: an in-process azuredevopsfake over TLS, whose certificate
// they trust through vcs.ca_file. Its organization's URL is
// {azureDevOpsOrg} to the scenario's requests.
type azureDevOpsSteps struct {
	w    *harness.World
	fake *azuredevopsfake.Fake
	srv  *azuredevopsfake.Server
}

func (b *azureDevOpsSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^an Azure DevOps user "(\w+)" with a personal access token kept as "(\w+)"$`, b.user)
	sc.Step(
		`^an Azure DevOps repository "([^"]+)" that "(\w+)" administers, its head of "main" kept as "(\w+)"$`,
		b.repository,
	)
	sc.Step(
		`^a commit to "([\w./-]+)" of the Azure DevOps repository "([^"]+)" is kept as "(\w+)"$`,
		b.commit,
	)
}

// start serves the fake and answers its certificate, which the caller
// writes to vcs.ca_file.
func (b *azureDevOpsSteps) start() []byte {
	b.fake = azuredevopsfake.New()
	b.srv = b.fake.Start()
	b.w.IDs["azureDevOpsOrg"] = b.srv.OrganizationURL
	return b.srv.CA
}

// VCS starts the fake for an @azure-devops scenario on first use, and returns the
// vcs settings and certificate every server of the scenario needs while
// it runs.
func (b *azureDevOpsSteps) VCS(
	_ context.Context,
	w *harness.World,
) (map[string]any, []byte, error) {
	if slices.Contains(w.Tags, "@azure-devops") && b.srv == nil {
		return nil, b.start(), nil
	}
	if b.srv != nil {
		return nil, b.srv.CA, nil
	}
	return nil, nil, nil
}

// Stop stops the fake, if the scenario started it.
func (b *azureDevOpsSteps) Stop() {
	if b.srv != nil {
		b.srv.Close()
	}
}

// user issues username a personal access token with every scope and
// keeps it as key.
func (b *azureDevOpsSteps) user(username, key string) error {
	if b.fake == nil {
		return fmt.Errorf(
			"%w: an Azure DevOps user needs an @azure-devops scenario",
			harness.ErrStep,
		)
	}
	b.w.IDs[key] = b.fake.AddToken(username)
	return nil
}

// repository makes username an administrator of path's project, path
// being a project and a repository name, with one commit on main of its
// repository, kept as key.
func (b *azureDevOpsSteps) repository(path, username, key string) error {
	project, slug, err := b.split(path)
	if err != nil {
		return err
	}
	b.fake.Grant(project, username, azuredevopsfake.Administrator)
	b.w.IDs[key] = b.fake.Repo(project, slug).Commit("main", "first", map[string]string{
		"main.tf": "# first\n",
	})
	return nil
}

// commit adds a commit to branch of path, which a new branch starts
// from the last commit, and keeps it as key.
func (b *azureDevOpsSteps) commit(branch, path, key string) error {
	project, slug, err := b.split(path)
	if err != nil {
		return err
	}
	b.w.IDs[key] = b.fake.Repo(project, slug).Commit(branch, key, map[string]string{
		"main.tf": "# " + key + "\n",
	})
	return nil
}

// split parts path into its project and the repository's name.
func (b *azureDevOpsSteps) split(path string) (project, slug string, err error) {
	project, slug, ok := strings.Cut(path, "/")
	if b.fake == nil || !ok || project == "" || slug == "" {
		return "", "", fmt.Errorf(
			"%w: no Azure DevOps repository %q in this scenario",
			harness.ErrStep,
			path,
		)
	}
	return project, slug, nil
}
