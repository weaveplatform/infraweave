package vcs

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"

	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitLivePolicyProgramsCompileWithActualOPA(t *testing.T) {
	const path = "../../../features/vcs/feedback.feature"
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	document, err := gherkin.ParseGherkinDocument(file, (&messages.Incrementing{}).NewId)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	request := regexp.MustCompile(`^token "h" sends "POST /api/v1/policies" with '(.*)' through A$`)
	checked := map[string]bool{}
	for _, scenario := range gherkin.Pickles(*document, path, (&messages.Incrementing{}).NewId) {
		live := false
		for _, tag := range scenario.Tags {
			live = live || tag.Name == "@case:policy-live-exit"
		}
		if !live {
			continue
		}
		for _, step := range scenario.Steps {
			match := request.FindStringSubmatch(step.Text)
			if len(match) == 0 || checked[match[1]] {
				continue
			}
			var input struct {
				Type   rego.Type       `json:"type"`
				Engine rego.EngineType `json:"engineType"`
				Body   string          `json:"body"`
			}
			if err := json.Unmarshal([]byte(match[1]), &input); err != nil {
				t.Fatal(err)
			}
			if err := evaluator.Check(t.Context(), rego.Policy{Type: input.Type, Engine: input.Engine, Body: input.Body}); err != nil {
				t.Fatalf("live workflow's authored program does not compile: %v", err)
			}
			checked[match[1]] = true
		}
	}
	if len(checked) == 0 {
		t.Fatal("no live workflow programs were checked")
	}
	t.Logf("compiled %d authored live workflow programs; upstream execution remains the vcs-lab gate", len(checked))
}

func TestUnitLiveLabIgnoresInheritedFakeTags(t *testing.T) {
	w := &harness.World{
		IDs:  map[string]string{},
		Tags: []string{"@vcs-lab", "@vcs", "@github", "@provider:github-public", "@gitlab", "@bitbucket-cloud", "@bitbucket-dc", "@azure-devops"},
	}
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
