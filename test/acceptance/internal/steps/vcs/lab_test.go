package vcs

import (
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitLiveLabIgnoresInheritedFakeTags(t *testing.T) {
	w := &harness.World{IDs: map[string]string{}, Tags: []string{"@vcs-lab", "@vcs", "@github", "@provider:github-public", "@gitlab", "@bitbucket-cloud", "@bitbucket-dc", "@azure-devops"}}
	for name, fake := range map[string]harness.Fake{
		"github":          &githubSteps{w: w},
		"gitlab":          &gitlabSteps{w: w},
		"bitbucket-cloud": &bitbucketCloudSteps{w: w},
		"bitbucket-dc":    &bitbucketDCSteps{w: w},
		"azure-devops":    &azureDevOpsSteps{w: w},
	} {
		t.Run(name, func(t *testing.T) {
			defer fake.Stop()
			settings, ca, err := fake.VCS(t.Context(), w)
			if err != nil || len(settings) != 0 || len(ca) != 0 {
				t.Fatalf("live lab inherited fake configuration: settings=%v CA bytes=%d error=%v", settings, len(ca), err)
			}
		})
	}
}
