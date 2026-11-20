package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

const captureProbe = "INFRAWEAVE_CAPTURE_REPLAY_PROBE"

func TestUnitFeatureReplayRequiresExecutedScenario(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success bool
	}{
		{"success", true},
		{"empty", false},
		{"skipped", false},
		{"failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			mainReport := filepath.Join(directory, "cucumber.json")
			if err := os.WriteFile(mainReport, []byte("ordinary partition report"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestUnitFeatureReplayProbe$", "-test.count=1")
			cmd.Env = append(os.Environ(), captureProbe+"="+tc.name, "INFRAWEAVE_CAPTURE_REPLAY_DIRECTORY="+directory,
				EnvPartition+"=core", EnvTags+"=@never-select", EnvFeatures+"=/no/such/features")
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("capture execution success %t, want %t: %s", err == nil, tc.success, output)
			}
			if !tc.success && !strings.Contains(string(output), "capture fixture executed 0/1 scenarios") {
				t.Fatalf("probe failed outside the replay assertion: %s", output)
			}
			data, err := os.ReadFile(mainReport)
			if err != nil || string(data) != "ordinary partition report" {
				t.Fatal("capture replay overwrote the ordinary partition report")
			}
			if _, err := os.Stat(filepath.Join(directory, "capture-replays", "fixture.json")); err != nil {
				t.Fatal("capture replay retained no dedicated scenario report")
			}
		})
	}
}

func TestUnitFeatureReplayProbe(t *testing.T) {
	kind := os.Getenv(captureProbe)
	if kind == "" {
		t.Skip("isolated replay refusal probe")
	}
	directory := os.Getenv("INFRAWEAVE_CAPTURE_REPLAY_DIRECTORY")
	s := &Suite{Out: directory, CoverDir: directory, cucumber: filepath.Join(directory, "cucumber.json"), skipped: map[string][]string{}}
	s.areas = []func(*godog.ScenarioContext, *World){func(sc *godog.ScenarioContext, _ *World) {
		sc.Step(`^a capture fixture$`, func(context.Context) error {
			switch kind {
			case "skipped":
				return godog.ErrSkip
			case "failed":
				return ErrStep
			default:
				return nil
			}
		})
	}}
	contents := "Feature: fixture\n"
	if kind != "empty" {
		contents += "  Scenario: expected\n    Given a capture fixture\n"
	}
	(&FeatureReplay{suite: s}).Run(t, "fixture", []byte(contents), []string{"expected"})
}
