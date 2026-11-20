package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/deps"
)

// FeatureReplay shares the real worker/tool dependencies across a capture
// pass. Its explicit feature input does not inherit the ordinary partition's
// tag filter, and its reports cannot overwrite that partition's Cucumber file.
type FeatureReplay struct{ suite *Suite }

// NewFeatureReplay owns its dependencies until the parent capture test ends.
func NewFeatureReplay(t *testing.T, areas []func(*godog.ScenarioContext, *World)) *FeatureReplay {
	t.Helper()
	s := newSuite(t)
	s.areas = areas
	s.requireDocker = true
	s.dockerErr = deps.DockerReachable(t.Context())
	if s.dockerErr != nil {
		t.Fatalf("capture workflow requires actual Docker: %v", s.dockerErr)
	}
	s.Out = filepath.Join(s.Out, "capture-replays")
	t.Cleanup(func() {
		if err := s.Deps.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return &FeatureReplay{suite: s}
}

// Run requires every expected scenario to finish, including area teardown.
// An empty selection, skipped scenario or duplicate name cannot prove a replay.
func (r *FeatureReplay) Run(t *testing.T, id string, contents []byte, names []string) {
	t.Helper()
	if len(names) == 0 || filepath.Base(id) != id {
		t.Fatal("capture workflow needs a safe identity and expected scenarios")
	}
	expected := map[string]bool{}
	for _, name := range names {
		if name == "" || expected[name] {
			t.Fatal("capture workflow has an empty or repeated scenario name")
		}
		expected[name] = true
	}
	completed := map[string]bool{}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	directory := filepath.Join(r.suite.CoverDir, "capture-replays")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	status := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			r.suite.initScenario(sc)
			sc.After(func(ctx context.Context, scenario *godog.Scenario, err error) (context.Context, error) {
				if !expected[scenario.Name] || completed[scenario.Name] {
					return ctx, fmt.Errorf("%w: unexpected or duplicate capture scenario", ErrStep)
				}
				if err == nil {
					completed[scenario.Name] = true
				}
				return ctx, err
			})
		},
		Options: &godog.Options{
			Format:          "pretty,cucumber:" + filepath.Join(directory, id+".json"),
			FeatureContents: []godog.Feature{{Name: "captures/" + id + "/replay.feature", Contents: contents}},
			TestingT:        t,
			DefaultContext:  ctx,
			Strict:          true,
			Concurrency:     1,
		},
	}.Run()
	if status != 0 || len(completed) != len(expected) {
		t.Fatalf("capture %s executed %d/%d scenarios; status %d", id, len(completed), len(expected), status)
	}
}
