package acceptance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

// scenarioTags parses every feature under dir and returns each scenario's
// tags, its feature's included, keyed by "file: scenario".
func scenarioTags(t *testing.T, dir string) map[string][]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.feature"))
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
			out[filepath.Base(p)+": "+pk.Name] = tags
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
		if _, err := Place(tags); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	unlisted := 0
	for name, tags := range scenarioTags(t, filepath.Join("testdata", "partitions")) {
		if _, err := Place(tags); errors.Is(err, ErrUnlistedTag) {
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
		{[]string{"@ai"}, "integrations"},
		{[]string{"@provider:resource:spacelift_stack", "@terraform"}, "runs"},
	} {
		got, err := Place(tc.tags)
		if err != nil || got != tc.want {
			t.Errorf("Place(%q) = %q, %v; want %q", tc.tags, got, err, tc.want)
		}
	}
	if _, err := Place([]string{"@postgres", "@redis"}); !errors.Is(err, ErrUnlistedTag) {
		t.Errorf("an unlisted tag placed: %v", err)
	}
}

func TestUnitTagExpression(t *testing.T) {
	for name, want := range map[string]string{
		"":     "",
		"runs": "@terraform,@tofu,@kubernetes,@flows-agent",
		"integrations": "~@terraform && ~@tofu && ~@kubernetes && ~@flows-agent && " +
			"@vcs,@github,@gitlab,@ai",
		"core": "~@terraform && ~@tofu && ~@kubernetes && ~@flows-agent && ~@vcs && ~@github && ~@gitlab && ~@ai",
	} {
		got, err := TagExpression(name)
		if err != nil || got != want {
			t.Errorf("TagExpression(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := TagExpression("everything"); !errors.Is(err, ErrUnknownPartition) {
		t.Errorf("an unknown partition: %v", err)
	}
}

func TestUnitDockerTags(t *testing.T) {
	got := dockerTags([]string{"@postgres", "@oidc", "@ai", "@op:stacks", "@vcs"})
	if !slices.Equal(got, []string{"@postgres", "@vcs"}) {
		t.Errorf("dockerTags = %q", got)
	}
}

func TestUnitSlug(t *testing.T) {
	if got := slug(
		"A failed scenario's evidence, @postgres!",
	); got != "a-failed-scenario-s-evidence-postgres" {
		t.Errorf("slug = %q", got)
	}
}

// TestUnitEventually runs in virtual time, so its deadlines cost nothing.
func TestUnitEventually(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		polls := 0
		err := Eventually(t.Context(), "the third poll", func(context.Context) (bool, error) {
			polls++
			return polls == 3, nil
		})
		if err != nil || polls != 3 {
			t.Errorf("Eventually = %v after %d polls", err, polls)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		last := errors.New("connection refused")
		err := Eventually(t.Context(), "a dependency", func(context.Context) (bool, error) {
			return false, last
		})
		if !errors.Is(err, ErrTimedOut) || !errors.Is(err, last) ||
			err.Error() != "waiting for a dependency: timed out (last: connection refused)" {
			t.Errorf("Eventually = %v", err)
		}
		if elapsed := time.Since(start); elapsed < waitLimit {
			t.Errorf("gave up after %s, before the %s limit", elapsed, waitLimit)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err := Eventually(ctx, "nothing", func(context.Context) (bool, error) { return false, nil })
		if err == nil || err.Error() != "waiting for nothing: timed out" {
			t.Errorf("Eventually = %v", err)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		polls := 0
		err := Eventually(t.Context(), "a process", func(context.Context) (bool, error) {
			polls++
			return false, errPermanent
		})
		if !errors.Is(err, errPermanent) || polls != 1 {
			t.Errorf("a permanent error polled %d times: %v", polls, err)
		}
	})
}
