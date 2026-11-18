package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

func TestUnitScenarioCleanupFollowsAreaHooks(t *testing.T) {
	for _, failure := range []string{"none", "step", "area"} {
		t.Run(failure, func(t *testing.T) {
			cause := errors.New("original " + failure + " failure")
			var output bytes.Buffer
			var dir string
			areaRan, areaHadLiveDirectory := false, false
			s := &Suite{Out: t.TempDir()}
			s.areas = []func(*godog.ScenarioContext, *World){func(sc *godog.ScenarioContext, w *World) {
				sc.Step(`^a resource owned by an area$`, func() error {
					dir = w.Dir
					if err := os.WriteFile(filepath.Join(dir, "resource"), []byte("still live"), 0o600); err != nil {
						return err
					}
					if failure == "step" {
						return cause
					}
					return nil
				})
				sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
					areaRan = true
					body, err := os.ReadFile(filepath.Join(w.Dir, "resource"))
					areaHadLiveDirectory = err == nil && string(body) == "still live"
					if !areaHadLiveDirectory {
						return ctx, fmt.Errorf("area resource removed before its cleanup: %w", err)
					}
					if failure == "area" {
						return ctx, cause
					}
					return ctx, nil
				})
			}}
			status := godog.TestSuite{ScenarioInitializer: s.initScenario, Options: &godog.Options{
				Format: "progress",
				Output: &output,
				Strict: true,
				FeatureContents: []godog.Feature{
					{Name: "cleanup.feature", Contents: []byte("Feature: cleanup\n  Scenario: area resources\n    Given a resource owned by an area\n")},
				},
			}}.Run()
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			if !areaRan || !areaHadLiveDirectory {
				t.Fatalf("world cleanup preceded area teardown: ran=%v live=%v; %s", areaRan, areaHadLiveDirectory, output.String())
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("world cleanup did not run after the area hook", err)
			}
			if failure == "none" {
				if status != 0 {
					t.Fatal("successful cleanup failed", output.String())
				}
			} else if status == 0 || !strings.Contains(output.String(), cause.Error()) {
				t.Fatal("cleanup hid the original failure", output.String())
			}
			if failure != "none" {
				if _, err := os.Stat(filepath.Join(s.Out, "area-resources")); err != nil {
					t.Fatal("failure evidence was not captured before final removal", err)
				}
			}
		})
	}
}
