package acceptance

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
)

// azureDevOpsSteps run the Azure DevOps an @azure-devops scenario's
// servers reach: an in-process azuredevopsfake over TLS, whose certificate
// they trust through vcs.ca_file. Its organization's URL is
// {azureDevOpsOrg} to the scenario's requests.
type azureDevOpsSteps struct {
	fake *azuredevopsfake.Fake
	srv  *azuredevopsfake.Server
	// ids are the scenario's kept values, which the requests name as
	// {key}, shared with the organization steps.
	ids map[string]string
}

func (b *azureDevOpsSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^an Azure DevOps user "(\w+)" with a personal access token kept as "(\w+)"$`, b.user)
	sc.Step(
		`^an Azure DevOps repository "([^"]+)" that "(\w+)" administers, its head of "main" kept as "(\w+)"$`,
		b.repository,
	)
	sc.Step(
		`^a commit to "main" of the Azure DevOps repository "([^"]+)" is kept as "(\w+)"$`,
		b.commit,
	)
}

// start serves the fake and answers its certificate, which the caller
// writes to vcs.ca_file.
func (b *azureDevOpsSteps) start() []byte {
	b.fake = azuredevopsfake.New()
	b.srv = b.fake.Start()
	b.ids["azureDevOpsOrg"] = b.srv.OrganizationURL
	return b.srv.CA
}

// stop stops the fake, if the scenario started it.
func (b *azureDevOpsSteps) stop() {
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
			errStep,
		)
	}
	b.ids[key] = b.fake.AddToken(username)
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
	b.ids[key] = b.fake.Repo(project, slug).Commit("main", "first", map[string]string{
		"main.tf": "# first\n",
	})
	return nil
}

// commit adds a commit to main of path and keeps it as key.
func (b *azureDevOpsSteps) commit(path, key string) error {
	project, slug, err := b.split(path)
	if err != nil {
		return err
	}
	b.ids[key] = b.fake.Repo(project, slug).Commit("main", key, map[string]string{
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
			errStep,
			path,
		)
	}
	return project, slug, nil
}
