package acceptance

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
)

// gitlabSteps run the GitLab a @gitlab scenario's servers reach: an
// in-process gitlabfake over TLS, whose certificate they trust through
// vcs.ca_file. Its URL is {gitlabHost} to the scenario's requests.
type gitlabSteps struct {
	fake *gitlabfake.Fake
	srv  *gitlabfake.Server
	// ids are the scenario's kept values, which the requests name as
	// {key}, shared with the organization steps.
	ids map[string]string
}

func (g *gitlabSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a GitLab user "(\w+)" with a token kept as "(\w+)"$`, g.user)
	sc.Step(
		`^a GitLab project "([^"]+)" that "(\w+)" maintains, its head of "main" kept as "(\w+)"$`,
		g.project,
	)
	sc.Step(
		`^a commit to "([\w./-]+)" of the GitLab project "([^"]+)" is kept as "(\w+)"$`,
		g.commit,
	)
}

// start serves the fake and answers its certificate, which the caller
// writes to vcs.ca_file.
func (g *gitlabSteps) start() []byte {
	g.fake = gitlabfake.New()
	g.srv = g.fake.Start()
	g.ids["gitlabHost"] = g.srv.URL
	return g.srv.CA
}

// stop stops the fake, if the scenario started it.
func (g *gitlabSteps) stop() {
	if g.srv != nil {
		g.srv.Close()
	}
}

// user issues username a token with the api scope and keeps it as key.
func (g *gitlabSteps) user(username, key string) error {
	if g.fake == nil {
		return fmt.Errorf("%w: a GitLab user needs a @gitlab scenario", errStep)
	}
	g.ids[key] = g.fake.AddToken(username, "api")
	return nil
}

// project makes username a maintainer of path, a namespace and a project
// name, with one commit on main, kept as key.
func (g *gitlabSteps) project(path, username, key string) error {
	namespace, name, err := g.split(path)
	if err != nil {
		return err
	}
	g.fake.AddMember(namespace, name, username, gitlabfake.Maintainer)
	g.ids[key] = g.fake.Repo(namespace, name).Commit("main", "first", map[string]string{
		"main.tf": "# first\n",
	})
	return nil
}

// commit adds a commit to branch of path, which a new branch starts
// from the last commit, and keeps it as key.
func (g *gitlabSteps) commit(branch, path, key string) error {
	namespace, name, err := g.split(path)
	if err != nil {
		return err
	}
	g.ids[key] = g.fake.Repo(namespace, name).Commit(branch, key, map[string]string{
		"main.tf": "# " + key + "\n",
	})
	return nil
}

// split parts path into its namespace and the project's name.
func (g *gitlabSteps) split(path string) (namespace, name string, err error) {
	i := strings.LastIndex(path, "/")
	if g.fake == nil || i <= 0 {
		return "", "", fmt.Errorf("%w: no GitLab project %q in this scenario", errStep, path)
	}
	return path[:i], path[i+1:], nil
}
