package acceptance

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// scenarioTags parses every feature under dir and returns each scenario's
// tags, its feature's included, keyed by "path: scenario", the path below dir.
func scenarioTags(t *testing.T, dir string) map[string][]string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if strings.HasSuffix(p, ".feature") {
			paths = append(paths, p)
		}
		return err
	})
	if err != nil || len(paths) == 0 {
		t.Fatalf("no features in %s: %v", dir, err)
	}
	out := map[string][]string{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := gherkin.ParseGherkinDocument(f, (&messages.Incrementing{}).NewId)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for _, pk := range gherkin.Pickles(*doc, p, (&messages.Incrementing{}).NewId) {
			var tags []string
			for _, tg := range pk.Tags {
				tags = append(tags, tg.Name)
			}
			rel, _ := filepath.Rel(dir, p)
			out[filepath.ToSlash(rel)+": "+pk.Name] = tags
		}
	}
	return out
}

// TestUnitFeaturePartitions holds every scenario of features/ to the
// Partitions: a tag no partition lists would leave its scenario in no CI
// job, or in one without its dependency.
//
// ref: decision 0016
func TestUnitFeaturePartitions(t *testing.T) {
	for name, tags := range scenarioTags(t, "features") {
		if _, err := harness.Place(tags); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	unlisted := 0
	for name, tags := range scenarioTags(t, filepath.Join("testdata", "partitions")) {
		if _, err := harness.Place(tags); errors.Is(err, harness.ErrUnlistedTag) {
			unlisted++
		} else if err != nil || name != "unlisted.feature: A listed scenario" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if unlisted != 1 {
		t.Errorf("the fixture's unlisted tag fails %d scenarios, want 1", unlisted)
	}
}

func TestUnitPlace(t *testing.T) {
	for _, tc := range []struct {
		tags []string
		want string
	}{
		{nil, "core"},
		{[]string{"@op:stacks", "@case:denial", "@quarantine"}, "core"},
		{[]string{"@postgres"}, "core"},
		{[]string{"@oidc", "@objectstore"}, "core"},
		{[]string{"@postgres", "@vault"}, "core"},
		{[]string{"@postgres", "@terraform"}, "runs"},
		{[]string{"@vcs", "@postgres"}, "integrations"},
		{[]string{"@vcs", "@tofu"}, "runs"},
		{[]string{"@postgres", "@vcs", "@docker"}, "runs"},
		{[]string{"@ai"}, "integrations"},
		{[]string{"@provider:resource:spacelift_stack", "@terraform"}, "runs"},
		{[]string{"@postgres", "@vcs", "@vcs-lab", "@lab:gitlab"}, "vcs-lab"},
	} {
		got, err := harness.Place(tc.tags)
		if err != nil || got != tc.want {
			t.Errorf("Place(%q) = %q, %v; want %q", tc.tags, got, err, tc.want)
		}
	}
	if _, err := harness.Place(
		[]string{"@postgres", "@redis"},
	); !errors.Is(
		err,
		harness.ErrUnlistedTag,
	) {
		t.Errorf("an unlisted tag placed: %v", err)
	}
}

func TestUnitTagExpression(t *testing.T) {
	for name, want := range map[string]string{
		"":        "~@vcs-lab",
		"vcs-lab": "@vcs-lab",
		"runs":    "~@vcs-lab && @docker,@terraform,@tofu,@kubernetes,@flows-agent",
		"integrations": "~@vcs-lab && ~@docker && ~@terraform && ~@tofu && ~@kubernetes && ~@flows-agent && " +
			"@vcs,@github,@gitlab,@bitbucket-cloud,@bitbucket-dc,@azure-devops,@ai",
		"core": "~@vcs-lab && ~@docker && ~@terraform && ~@tofu && ~@kubernetes && ~@flows-agent && ~@vcs && ~@github && ~@gitlab && ~@bitbucket-cloud && ~@bitbucket-dc && ~@azure-devops && ~@ai",
	} {
		got, err := harness.TagExpression(name)
		if err != nil || got != want {
			t.Errorf("TagExpression(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := harness.TagExpression("everything"); !errors.Is(err, harness.ErrUnknownPartition) {
		t.Errorf("an unknown partition: %v", err)
	}
	for want, exprs := range map[string][]string{
		"":                        {"", ""},
		"@vcs-lab":                {"@vcs-lab", ""},
		"@vcs-lab && @lab:gitlab": {"@vcs-lab", "", "@lab:gitlab"},
	} {
		if got := harness.And(exprs...); got != want {
			t.Errorf("And(%q) = %q, want %q", exprs, got, want)
		}
	}
}

func TestUnitDockerTags(t *testing.T) {
	got := harness.DockerTags([]string{"@postgres", "@oidc", "@ai", "@op:stacks", "@vcs"})
	if !slices.Equal(got, []string{"@postgres", "@vcs"}) {
		t.Errorf("dockerTags = %q", got)
	}
}

func TestUnitSlug(t *testing.T) {
	if got := harness.Slug(
		"A failed scenario's evidence, @postgres!",
	); got != "a-failed-scenario-s-evidence-postgres" {
		t.Errorf("slug = %q", got)
	}
}
