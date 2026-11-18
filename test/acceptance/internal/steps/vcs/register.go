package vcs

import (
	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the vcs area's steps on sc, each step group sharing
// the scenario's World w. Each provider's steps are also one of w.Fakes,
// which starts the stand-in the scenario's tags name before its servers.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	git := &giteaSteps{w: w}
	github := &githubSteps{w: w}
	gitlab := &gitlabSteps{w: w}
	bitbucketCloud := &bitbucketCloudSteps{w: w}
	bitbucketDC := &bitbucketDCSteps{w: w}
	azureDevOps := &azureDevOpsSteps{w: w}
	lab := &labSteps{w: w}
	w.Fakes = append(w.Fakes, git, github, gitlab, bitbucketCloud, bitbucketDC, azureDevOps, lab)
	git.register(sc)
	github.register(sc)
	gitlab.register(sc)
	bitbucketCloud.register(sc)
	bitbucketDC.register(sc)
	azureDevOps.register(sc)
	lab.register(sc)
	(&vcsSteps{w: w}).register(sc)
	registerAccountSteps(sc, w)
	(&webhookSteps{w: w}).register(sc)
	registerOptionSteps(sc, w)
	(&webhookControlSteps{w: w, gitlab: gitlab, cloud: bitbucketCloud, dc: bitbucketDC, azure: azureDevOps, github: github}).register(sc)
}
