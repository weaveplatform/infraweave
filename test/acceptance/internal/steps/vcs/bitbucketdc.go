package vcs

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// bitbucketDCSteps run the Bitbucket Data Center a @bitbucket-dc
// scenario's servers reach: an in-process bitbucketdcfake over TLS, whose
// certificate they trust through vcs.ca_file. Its URL is
// {bitbucketDCHost} to the scenario's requests.
type bitbucketDCSteps struct {
	w    *harness.World
	fake *bitbucketdcfake.Fake
	srv  *bitbucketdcfake.Server
}

func (b *bitbucketDCSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a Bitbucket Data Center user "(\w+)" with an access token kept as "(\w+)"$`, b.user)
	sc.Step(
		`^a Bitbucket Data Center repository "([^"]+)" that "(\w+)" administers, its head of "main" kept as "(\w+)"$`,
		b.repository,
	)
	sc.Step(
		`^a commit to "([\w./-]+)" of the Bitbucket Data Center repository "([^"]+)" is kept as "(\w+)"$`,
		b.commit,
	)
}

// start serves the fake and answers its certificate, which the caller
// writes to vcs.ca_file.
func (b *bitbucketDCSteps) start() []byte {
	b.fake = bitbucketdcfake.New()
	b.srv = b.fake.Start()
	b.w.IDs["bitbucketDCHost"] = b.srv.URL
	return b.srv.CA
}

// VCS starts the fake for a @bitbucket-dc scenario on first use, and returns the
// vcs settings and certificate every server of the scenario needs while
// it runs.
func (b *bitbucketDCSteps) VCS(
	_ context.Context,
	w *harness.World,
) (map[string]any, []byte, error) {
	if slices.Contains(w.Tags, "@bitbucket-dc") && b.srv == nil {
		return nil, b.start(), nil
	}
	if b.srv != nil {
		return nil, b.srv.CA, nil
	}
	return nil, nil, nil
}

// Stop stops the fake, if the scenario started it.
func (b *bitbucketDCSteps) Stop() {
	if b.srv != nil {
		b.srv.Close()
	}
}

// user issues username an HTTP access token and keeps it as key.
func (b *bitbucketDCSteps) user(username, key string) error {
	if b.fake == nil {
		return fmt.Errorf(
			"%w: a Bitbucket Data Center user needs a @bitbucket-dc scenario",
			harness.ErrStep,
		)
	}
	b.w.IDs[key] = b.fake.AddToken(username)
	return nil
}

// repository makes username an admin of path, a project key and a
// repository slug, with one commit on main, kept as key.
func (b *bitbucketDCSteps) repository(path, username, key string) error {
	project, slug, err := b.split(path)
	if err != nil {
		return err
	}
	b.fake.Grant(project, slug, username, bitbucketdcfake.Admin)
	b.w.IDs[key] = b.fake.Repo(project, slug).Commit("main", "first", map[string]string{
		"main.tf": "# first\n",
	})
	return nil
}

// commit adds a commit to branch of path, which a new branch starts
// from the last commit, and keeps it as key.
func (b *bitbucketDCSteps) commit(branch, path, key string) error {
	project, slug, err := b.split(path)
	if err != nil {
		return err
	}
	b.w.IDs[key] = b.fake.Repo(project, slug).Commit(branch, key, map[string]string{
		"main.tf": "# " + key + "\n",
	})
	return nil
}

// split parts path into its project key and the repository's slug.
func (b *bitbucketDCSteps) split(path string) (project, slug string, err error) {
	project, slug, ok := strings.Cut(path, "/")
	if b.fake == nil || !ok || project == "" || slug == "" {
		return "", "", fmt.Errorf(
			"%w: no Bitbucket Data Center repository %q in this scenario",
			harness.ErrStep,
			path,
		)
	}
	return project, slug, nil
}
