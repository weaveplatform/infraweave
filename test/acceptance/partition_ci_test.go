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
	Name     string   `yaml:"name"`
	If       string   `yaml:"if"`
	Needs    []string `yaml:"needs"`
	Strategy struct {
		FailFast bool            `yaml:"fail-fast"` //nolint:tagliatelle // GitHub Actions field
		Matrix   partitionMatrix `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []partitionStep `yaml:"steps"`
}

type partitionMatrix struct {
	Partition []string `yaml:"partition"`
	Include   []struct {
		Partition string `yaml:"partition"`
		Timeout   int    `yaml:"timeout"`
	} `yaml:"include"`
	Expression string
}

func (m *partitionMatrix) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&m.Expression) //nolint:wrapcheck // YAML decoder preserves the workflow location
	}
	type literal partitionMatrix
	return node.Decode((*literal)(m)) //nolint:wrapcheck // YAML decoder preserves the workflow location
}

type partitionStep struct {
	ID              string            `yaml:"id"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"` //nolint:tagliatelle // GitHub Actions field
	Uses            string            `yaml:"uses"`
	Shell           string            `yaml:"shell"`
	Run             string            `yaml:"run"`
	With            map[string]string `yaml:"with"`
	Env             map[string]string `yaml:"env"`
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
		if strings.HasPrefix(partition.Name, "terraform") || partition.Name == "runs" || partition.Name == "run-credentials" {
			stress = append(stress, partition.Name)
		}
	}
	actual := make([]string, 0, len(quality.Jobs["accept"].Strategy.Matrix.Include))
	for _, job := range quality.Jobs["accept"].Strategy.Matrix.Include {
		actual = append(actual, job.Partition)
		if job.Timeout <= 60 {
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
	for name, workflow := range map[string]partitionWorkflow{"required": quality, "nightly": nightly} {
		found := slices.ContainsFunc(workflow.Jobs["accept"].Steps, func(step partitionStep) bool {
			return step.Run == "make accept PARTITION=${{ matrix.partition }}" && step.Env["INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"] == "1"
		})
		if !found {
			t.Errorf("%s acceptance can omit a registered workload or silently skip missing Docker", name)
		}
	}
}

func TestUnitCINativeAcceptanceIsRequired(t *testing.T) {
	quality := partitionWorkflowAt(t, "quality-gate.yml")
	nightly := partitionWorkflowAt(t, "nightly.yml")
	for name, workflow := range map[string]partitionWorkflow{"required": quality, "nightly": nightly} {
		native, ok := workflow.Jobs["native"]
		if !ok {
			t.Errorf("%s omits native acceptance and capture replays", name)
			continue
		}
		if !slices.ContainsFunc(native.Steps, func(step partitionStep) bool {
			return step.Run == "make accept-native" && step.Env["INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"] == "1"
		}) {
			t.Errorf("%s native acceptance must run every non-feature TestAcc and require Docker", name)
		}
	}
	for _, name := range []string{"gate", "coverage"} {
		if !slices.Contains(quality.Jobs[name].Needs, "native") {
			t.Errorf("%s can pass without native acceptance", name)
		}
	}
	for _, required := range []struct{ key, value string }{
		{"name", "cover-native"},
		{"path", "cover/native"},
	} {
		if !slices.ContainsFunc(quality.Jobs["native"].Steps, func(step partitionStep) bool {
			return step.With[required.key] == required.value
		}) {
			t.Errorf("native coverage and capture reports are not retained via %s=%s", required.key, required.value)
		}
	}
	raw, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	_, recipe, ok := strings.Cut(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\naccept-native:\n")
	if !ok {
		t.Fatal("missing native acceptance target")
	}
	recipe, _, _ = strings.Cut(recipe, "\n\n")
	for _, required := range []string{"-run '^TestAcc'", "-skip '^TestAccFeatures$$'", "./...", "-timeout 60m", "/native"} {
		if !strings.Contains(recipe, required) {
			t.Errorf("native target omits %s", required)
		}
	}
}

func TestUnitNextPostgresPreservesNativeAndIdentityCoverage(t *testing.T) {
	workflow := partitionWorkflowAt(t, "quality-gate.yml")
	samePartitions(t, "next PostgreSQL", workflow.Jobs["postgres-next"].Strategy.Matrix.Partition, []string{"core", "identity", "native"})
	if !slices.Contains(workflow.Jobs["gate"].Needs, "postgres-next") {
		t.Error("quality gate can pass despite next PostgreSQL failure")
	}
	next := workflow.Jobs["postgres-next"]
	if !slices.ContainsFunc(next.Steps, func(step partitionStep) bool {
		return step.Env["INFRAWEAVE_POSTGRES"] == "next" && step.Env["INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"] == "1" &&
			strings.Contains(step.Run, "make accept-native") && strings.Contains(step.Run, "make accept PARTITION=")
	}) {
		t.Error("next PostgreSQL omits an acceptance workload")
	}
	nightly := partitionWorkflowAt(t, "nightly.yml")
	if !slices.Contains(nightly.Jobs["report"].Needs, "native") {
		t.Error("nightly native failures cannot reach the issue reporter")
	}
}

func TestUnitCIRecordsActualTestedRevision(t *testing.T) {
	workflow := partitionWorkflowAt(t, "quality-gate.yml")
	for _, name := range []string{"unit", "accept", "native", "fips-unit", "fips-tests", "postgres-next"} {
		job := workflow.Jobs[name]
		if !slices.ContainsFunc(job.Steps, func(step partitionStep) bool {
			return step.Shell == "bash" && (strings.Contains(step.Run, "git rev-parse HEAD") && strings.Contains(step.Run, "HEAD^{tree}") ||
				strings.Contains(step.Run, "python3 .github/fips_gate.py record")) &&
				step.Env["PR_HEAD"] != "" && step.Env["PR_BASE"] != ""
		}) {
			t.Errorf("%s omits Bash-safe source head, base or actual tested checkout/tree provenance", name)
		}
		if !slices.ContainsFunc(job.Steps, func(step partitionStep) bool {
			return strings.HasPrefix(step.With["name"], "validation-") && strings.HasSuffix(step.With["path"], "/validation/revision.json")
		}) {
			t.Errorf("%s does not retain tested-revision provenance", name)
		}
	}
}

func TestUnitCIFIPSUsesCatalogueCompleteSelectorsAndFailClosedAggregate(t *testing.T) {
	workflow := partitionWorkflowAt(t, "quality-gate.yml")
	unit, suites, aggregate := workflow.Jobs["fips-unit"], workflow.Jobs["fips-tests"], workflow.Jobs["fips-only"]
	if !slices.ContainsFunc(unit.Steps, func(step partitionStep) bool {
		return step.ID == "matrix" && strings.Contains(step.Run, "go run ./test/acceptance/cmd/fips-matrix") &&
			step.Env["GOFIPS140"] == "certified" && step.Env["GODEBUG"] == "fips140=only"
	}) || !slices.ContainsFunc(unit.Steps, func(step partitionStep) bool { return strings.Contains(step.Run, "make test FIPS=1") }) {
		t.Error("FIPS unit suite or catalogue producer is missing")
	}
	if suites.Strategy.Matrix.Expression != "${{ fromJSON(needs.fips-unit.outputs.matrix) }}" || suites.Strategy.FailFast {
		t.Error("FIPS matrix must execute every catalogue job without cancelling siblings")
	}
	samePartitions(t, "FIPS matrix prerequisites", suites.Needs, []string{"fips-unit"})
	if !slices.ContainsFunc(suites.Steps, func(step partitionStep) bool {
		return step.ID == "suite" && strings.Contains(step.Run, "make accept-native FIPS=1") &&
			strings.Contains(step.Run, `make accept FIPS=1 PARTITION="$PARTITION"`) &&
			step.Env["PARTITION"] == "${{ matrix.partition }}" && step.Env["SHARD"] == "${{ matrix.shard }}" &&
			step.Env["INFRAWEAVE_ACCEPT_REQUIRE_DOCKER"] == "1" && step.Env["INFRAWEAVE_ACCEPT_PROGRESS"] == "1" &&
			strings.Contains(step.Run, "accept_progress.py")
	}) {
		t.Error("FIPS matrix omits native/capture or explicit feature selectors, Docker refusal or progress")
	}
	if aggregate.Name != "fips-only" || aggregate.If != "always()" {
		t.Error("FIPS aggregate must preserve its required name and run after failures/skips")
	}
	samePartitions(t, "FIPS aggregate dependencies", aggregate.Needs, []string{"fips-unit", "fips-tests"})
	if !slices.Contains(workflow.Jobs["gate"].Needs, "fips-only") || !slices.ContainsFunc(aggregate.Steps, func(step partitionStep) bool {
		return strings.Contains(step.Run, "fips_gate.py check") && step.Shell == "bash" &&
			step.Env["FIPS_CHILDREN"] == "${{ toJSON(needs) }}" && step.Env["FIPS_MATRIX"] == "${{ needs.fips-unit.outputs.matrix }}"
	}) {
		t.Error("Required FIPS gate does not validate child outcomes and complete receipt membership")
	}
	for name, job := range map[string]partitionJob{"fips-unit": unit, "fips-tests": suites} {
		if !slices.ContainsFunc(job.Steps, func(step partitionStep) bool {
			return step.ID == "suite" && step.If == "" && !step.ContinueOnError
		}) {
			t.Errorf("%s can skip or ignore its required suite", name)
		}
		if !slices.ContainsFunc(job.Steps, func(step partitionStep) bool {
			return step.If == "always()" && step.With["if-no-files-found"] == "error" &&
				strings.HasPrefix(step.With["name"], "fips-result-") && strings.Contains(step.With["name"], "github.run_attempt")
		}) {
			t.Errorf("%s can omit its unique bounded execution receipt", name)
		}
	}
	raw, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"ACCEPT_FEATURES_ONLY = $(strip $(PARTITION))", "^TestAccFeatures$$,^TestAcc", "./test/acceptance,./...", "ACCEPT_TIMEOUT ?= $(if $(PARTITION),60m,120m)"} {
		if !strings.Contains(string(raw), selector) {
			t.Errorf("feature partitions can omit a newly registered catalogue entry or share an unbounded budget: missing %s", selector)
		}
	}
}
