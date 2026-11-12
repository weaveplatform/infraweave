package vcs

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// gitlabSteps run the GitLab a @gitlab scenario's servers reach: an
// in-process gitlabfake over TLS, whose certificate they trust through
// vcs.ca_file. Its URL is {gitlabHost} to the scenario's requests.
type gitlabSteps struct {
	w    *harness.World
	fake *gitlabfake.Fake
	srv  *gitlabfake.Server
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
	g.w.IDs["gitlabHost"] = g.srv.URL
	return g.srv.CA
}

// VCS starts the fake for a @gitlab scenario on first use, and returns the
// vcs settings and certificate every server of the scenario needs while
// it runs.
func (g *gitlabSteps) VCS(_ context.Context, w *harness.World) (map[string]any, []byte, error) {
	if slices.Contains(w.Tags, "@gitlab") && g.srv == nil {
		return nil, g.start(), nil
	}
	if g.srv != nil {
		return nil, g.srv.CA, nil
	}
	return nil, nil, nil
}

// Stop stops the fake, if the scenario started it.
func (g *gitlabSteps) Stop() {
	if g.srv != nil {
		g.srv.Close()
	}
}

// user issues username a token with the api scope and keeps it as key.
func (g *gitlabSteps) user(username, key string) error {
	if g.fake == nil {
		return fmt.Errorf("%w: a GitLab user needs a @gitlab scenario", harness.ErrStep)
	}
	g.w.IDs[key] = g.fake.AddToken(username, "api")
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
	g.w.IDs[key] = g.fake.Repo(namespace, name).Commit("main", "first", map[string]string{
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
	g.w.IDs[key] = g.fake.Repo(namespace, name).Commit(branch, key, map[string]string{
		"main.tf": "# " + key + "\n",
	})
	return nil
}

// split parts path into its namespace and the project's name.
func (g *gitlabSteps) split(path string) (namespace, name string, err error) {
	i := strings.LastIndex(path, "/")
	if g.fake == nil || i <= 0 {
		return "", "", fmt.Errorf(
			"%w: no GitLab project %q in this scenario",
			harness.ErrStep,
			path,
		)
	}
	return path[:i], path[i+1:], nil
}
