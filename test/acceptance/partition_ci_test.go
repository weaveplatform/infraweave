package acceptance

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type partitionWorkflow struct {
	Jobs map[string]partitionJob `yaml:"jobs"`
}

type partitionJob struct {
	Needs    []string `yaml:"needs"`
	Strategy struct {
		Matrix struct {
			Partition []string `yaml:"partition"`
			Include   []struct {
				Partition string `yaml:"partition"`
				Timeout   int    `yaml:"timeout"`
			} `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []partitionStep `yaml:"steps"`
}

type partitionStep struct {
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

func partitionWorkflowAt(t *testing.T, name string) partitionWorkflow {
	t.Helper()
	raw, err := os.ReadFile("../../.github/workflows/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var workflow partitionWorkflow
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func samePartitions(t *testing.T, name string, actual, expected []string) {
	t.Helper()
	actual, expected = slices.Clone(actual), slices.Clone(expected)
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Errorf("%s partitions=%v, require exactly %v", name, actual, expected)
	}
}

// A valid harness selector is insufficient if no required job actually runs
// it. Keep the registry, CI matrices and aggregate artifact gates in agreement.
func TestUnitCIPartitionsRunAndCollectEveryRegisteredWorkload(t *testing.T) {
	quality := partitionWorkflowAt(t, "quality-gate.yml")
	nightly := partitionWorkflowAt(t, "nightly.yml")
	expected := make([]string, 0, len(harness.Partitions))
	var stress []string
	for _, partition := range harness.Partitions {
		expected = append(expected, partition.Name)
		if strings.HasPrefix(partition.Name, "terraform") || partition.Name == "runs" {
			stress = append(stress, partition.Name)
		}
	}
	actual := make([]string, 0, len(quality.Jobs["accept"].Strategy.Matrix.Include))
	for _, job := range quality.Jobs["accept"].Strategy.Matrix.Include {
		actual = append(actual, job.Partition)
		if job.Timeout <= 10 {
			t.Errorf("accept-%s job budget %dm leaves no margin above the Go test deadline", job.Partition, job.Timeout)
		}
	}
	samePartitions(t, "required acceptance", actual, expected)
	samePartitions(t, "nightly acceptance", nightly.Jobs["accept"].Strategy.Matrix.Partition, expected)
	samePartitions(t, "worker stress", nightly.Jobs["stress"].Strategy.Matrix.Partition, stress)
	for _, gate := range []string{"gate", "coverage", "parity"} {
		if !slices.Contains(quality.Jobs[gate].Needs, "accept") {
			t.Errorf("%s does not depend on every acceptance matrix job", gate)
		}
	}
	for _, artifact := range []struct {
		job, key, value string
	}{
		{"accept", "name", "cover-accept-${{ matrix.partition }}"},
		{"accept", "path", "cover/accept"},
		{"coverage", "pattern", "cover-*"},
		{"parity", "pattern", "cover-accept-*"},
	} {
		found := slices.ContainsFunc(quality.Jobs[artifact.job].Steps, func(step partitionStep) bool { return step.With[artifact.key] == artifact.value })
		if !found {
			t.Errorf("%s does not collect every partition artifact via %s=%q", artifact.job, artifact.key, artifact.value)
		}
	}
	fipsAll := false
	for _, step := range quality.Jobs["fips-only"].Steps {
		lines := strings.Split(strings.TrimSpace(step.Run), "\n")
		fipsAll = fipsAll || lines[len(lines)-1] == "make accept FIPS=1" &&
			step.Env["INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"] == "1"
	}
	if !fipsAll {
		t.Error("FIPS job does not run the complete acceptance workload")
	}
	for name, workflow := range map[string]partitionWorkflow{"required": quality, "nightly": nightly} {
		found := slices.ContainsFunc(workflow.Jobs["accept"].Steps, func(step partitionStep) bool {
			return step.Run == "make accept PARTITION=${{ matrix.partition }}" && step.Env["INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"] == "1"
		})
		if !found {
			t.Errorf("%s acceptance can omit a registered workload or silently skip missing Docker", name)
		}
	}
}
