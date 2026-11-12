package vcs

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// bitbucketCloudSteps run the Bitbucket Cloud a @bitbucket-cloud
// scenario's servers reach: an in-process bitbucketcloudfake over TLS,
// whose certificate they trust through vcs.ca_file and whose API they
// reach through vcs.bitbucket_cloud.api_host.
type bitbucketCloudSteps struct {
	w    *harness.World
	fake *bitbucketcloudfake.Fake
	srv  *bitbucketcloudfake.Server
}

func (b *bitbucketCloudSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a Bitbucket Cloud account "([^"]+)" with an API token kept as "(\w+)"$`, b.account)
	sc.Step(
		`^a Bitbucket Cloud repository "([^"]+)" that "([^"]+)" administers, its head of "main" kept as "(\w+)"$`,
		b.repository,
	)
	sc.Step(
		`^a commit to "([\w./-]+)" of the Bitbucket Cloud repository "([^"]+)" is kept as "(\w+)"$`,
		b.commit,
	)
}

// start serves the fake and answers its certificate, which the caller
// writes to vcs.ca_file.
func (b *bitbucketCloudSteps) start() []byte {
	b.fake = bitbucketcloudfake.New()
	b.srv = b.fake.Start()
	return b.srv.CA
}

// apiHost is the fake's API, which vcs.bitbucket_cloud.api_host names.
func (b *bitbucketCloudSteps) apiHost() string { return b.srv.APIHost }

// VCS starts the fake for a @bitbucket-cloud scenario on first use, and returns the
// vcs settings and certificate every server of the scenario needs while
// it runs.
func (b *bitbucketCloudSteps) VCS(
	_ context.Context,
	w *harness.World,
) (map[string]any, []byte, error) {
	var ca []byte
	if slices.Contains(w.Tags, "@bitbucket-cloud") && b.srv == nil {
		ca = b.start()
	} else if b.srv != nil {
		ca = b.srv.CA
	}
	if b.srv == nil {
		return nil, nil, nil
	}
	return map[string]any{"bitbucket_cloud": map[string]any{"api_host": b.apiHost()}}, ca, nil
}

// Stop stops the fake, if the scenario started it.
func (b *bitbucketCloudSteps) Stop() {
	if b.srv != nil {
		b.srv.Close()
	}
}

// account issues email an API token and keeps it as key.
func (b *bitbucketCloudSteps) account(email, key string) error {
	if b.fake == nil {
		return fmt.Errorf(
			"%w: a Bitbucket Cloud account needs a @bitbucket-cloud scenario",
			harness.ErrStep,
		)
	}
	b.w.IDs[key] = b.fake.AddToken(email)
	return nil
}

// repository makes email an admin of path, a workspace and a repository
// slug, with one commit on main, kept as key.
func (b *bitbucketCloudSteps) repository(path, email, key string) error {
	workspace, slug, err := b.split(path)
	if err != nil {
		return err
	}
	b.fake.Grant(workspace, slug, email, bitbucketcloudfake.Admin)
	b.w.IDs[key] = b.fake.Repo(workspace, slug).Commit("main", "first", map[string]string{
		"main.tf": "# first\n",
	})
	return nil
}

// commit adds a commit to branch of path, which a new branch starts
// from the last commit, and keeps it as key.
func (b *bitbucketCloudSteps) commit(branch, path, key string) error {
	workspace, slug, err := b.split(path)
	if err != nil {
		return err
	}
	b.w.IDs[key] = b.fake.Repo(workspace, slug).Commit(branch, key, map[string]string{
		"main.tf": "# " + key + "\n",
	})
	return nil
}

// split parts path into its workspace and the repository's slug.
func (b *bitbucketCloudSteps) split(path string) (workspace, slug string, err error) {
	workspace, slug, ok := strings.Cut(path, "/")
	if b.fake == nil || !ok || workspace == "" || slug == "" {
		return "", "", fmt.Errorf(
			"%w: no Bitbucket Cloud repository %q in this scenario",
			harness.ErrStep,
			path,
		)
	}
	return workspace, slug, nil
}
