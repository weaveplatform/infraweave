package acceptance

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
)

// bitbucketDCSteps run the Bitbucket Data Center a @bitbucket-dc
// scenario's servers reach: an in-process bitbucketdcfake over TLS, whose
// certificate they trust through vcs.ca_file. Its URL is
// {bitbucketDCHost} to the scenario's requests.
type bitbucketDCSteps struct {
	fake *bitbucketdcfake.Fake
	srv  *bitbucketdcfake.Server
	// ids are the scenario's kept values, which the requests name as
	// {key}, shared with the organization steps.
	ids map[string]string
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
	b.ids["bitbucketDCHost"] = b.srv.URL
	return b.srv.CA
}

// stop stops the fake, if the scenario started it.
func (b *bitbucketDCSteps) stop() {
	if b.srv != nil {
		b.srv.Close()
	}
}

// user issues username an HTTP access token and keeps it as key.
func (b *bitbucketDCSteps) user(username, key string) error {
	if b.fake == nil {
		return fmt.Errorf(
			"%w: a Bitbucket Data Center user needs a @bitbucket-dc scenario",
			errStep,
		)
	}
	b.ids[key] = b.fake.AddToken(username)
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
	b.ids[key] = b.fake.Repo(project, slug).Commit("main", "first", map[string]string{
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
	b.ids[key] = b.fake.Repo(project, slug).Commit(branch, key, map[string]string{
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
			errStep,
			path,
		)
	}
	return project, slug, nil
}
