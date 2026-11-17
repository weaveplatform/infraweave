//go:build unix

package exec_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/backend"
	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// fakeTerragrunt is a terragrunt that appends its arguments, the tool it
// drives and the directory it writes the units' plans under to $TRACE;
// that planning copies $UNIT_PLANS there, as Terragrunt writes each
// unit's plan's JSON, when that directory is set and $UNIT_PLANS is one;
// and that exits with
// $EXIT_<command>, the command after --, 0 when unset.
const fakeTerragrunt = `#!/bin/sh
printf '%s\n' "terragrunt $* tf=$TG_TF_PATH ni=$TG_NON_INTERACTIVE json=${TG_JSON_OUT_DIR:+set}" >> "$TRACE"
while [ "$1" != -- ]; do shift; done
shift
if [ "$1" = plan ] && [ -n "$TG_JSON_OUT_DIR" ]; then
  [ -d "$UNIT_PLANS" ] && mkdir -p "$TG_JSON_OUT_DIR" && cp -R "$UNIT_PLANS"/. "$TG_JSON_OUT_DIR"/
  echo binary-plan > tfplan.tfplan
fi
eval "exit \${EXIT_$1:-0}"
`

// unitPlan is show -json of a unit's plan that changes one resource,
// deletes another and changes an output, in a state of three.
const unitPlan = `{"format_version":"1.2","resource_changes":[` +
	`{"address":"terraform_data.y","mode":"managed","change":{"actions":["update"]}},` +
	`{"address":"terraform_data.z","mode":"managed","change":{"actions":["delete"]}}],` +
	`"output_changes":{"o":{"actions":["update"]}},` +
	`"prior_state":{"values":{"root_module":{"resources":[` +
	`{"address":"terraform_data.x","mode":"managed"},` +
	`{"address":"terraform_data.y","mode":"managed"},` +
	`{"address":"terraform_data.z","mode":"managed"}]}}}}`

// terragruntWorld is a world whose run is a Terragrunt stack's with
// managed state: two units, one at the project root and one at a/b, the
// first planning planJSON and the second unitPlan.
func terragruntWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	bin := filepath.Join(filepath.Dir(w.workspace), "bin")
	if err := os.WriteFile(filepath.Join(bin, "terragrunt"), []byte(fakeTerragrunt),
		0o755); err != nil {
		t.Fatal(err)
	}
	plans := filepath.Join(filepath.Dir(w.workspace), "unit-plans")
	for name, content := range map[string]string{
		filepath.Join(plans, "tfplan.json"):                    planJSON,
		filepath.Join(plans, "a", "b", "tfplan.json"):          unitPlan,
		filepath.Join(w.workspace, "terragrunt.hcl"):           "",
		filepath.Join(w.workspace, "a", "b", "terragrunt.hcl"): "",
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.environ = append(w.environ, "UNIT_PLANS="+plans)
	w.h.Spec.Terragrunt = &wire.RunSpecTerragrunt{Version: "1.2.2"}
	w.h.Spec.StateBackend = &wire.RunSpecStateBackend{Path: "/state/v1/stacks/stack-1"}
	return w
}

// TestUnitExecTerragrunt checks a tracked Terragrunt run that waits for
// confirmation: initialization writes each unit's backend file, naming
// its workspace, and runs init across the units with the tool Terragrunt
// drives and the state backend's credentials in the environment; planning
// runs plan with TG_JSON_OUT_DIR naming where the units' plans go, set
// for that command alone, and reports the units' deltas summed; and the
// saved workspace, which holds a unit's plan, is the one artifact
// planning records.
func TestUnitExecTerragrunt(t *testing.T) {
	w := terragruntWorld(t)
	w.h.Spec.Hooks = map[string][]string{
		"before_init": {hook("env", "TF_HTTP_USERNAME", "TF_HTTP_PASSWORD", "TG_TF_PATH")},
		"after_plan":  {`printf '%s\n' "after_plan json=${TG_JSON_OUT_DIR:-unset}" >> "$TRACE"`},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
	})
	d := w.w.reports[2].Delta
	if d == nil || d.Add != 1 || d.Change != 1 || d.Delete != 1 || d.Import != 0 ||
		!d.Outputs || *d.Resources != 3 || *d.Move != 0 || *d.Forget != 0 {
		t.Errorf("planning's delta: %+v", d)
	}
	equal(t, "trace", w.traced(), []string{
		"env run run-token terraform",
		"terragrunt run --all -- init -input=false -lock-timeout=5m0s tf=terraform ni=true json=",
		"terragrunt run --all -- plan -input=false -lock-timeout=5m0s -lock=true " +
			"-out=tfplan.tfplan tf=terraform ni=true json=set",
		"after_plan json=unset",
	})
	for unit, address := range map[string]string{
		backend.File:          `"http://server.test/state/v1/stacks/stack-1"`,
		"a/b/" + backend.File: `"http://server.test/state/v1/stacks/stack-1/workspaces/a%2Fb"`,
	} {
		b, err := os.ReadFile(filepath.Join(w.workspace, filepath.FromSlash(unit)))
		if err != nil || !strings.Contains(string(b), "address        = "+address) {
			t.Errorf("%s = %s, %v", unit, b, err)
		}
	}
	if got := strings.Join(w.w.taken, "\n"); !strings.Contains(got,
		"artifacts 5: workspace workspace\n") || strings.Contains(got, exec.PlanArtifact) {
		t.Errorf("taken: %q", w.w.taken)
	}
	dir := filepath.Join(t.TempDir(), "restored")
	if err := workspace.Read(bytes.NewReader(w.w.recorded[exec.WorkspaceName]), dir, w.pool,
		runID, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "tfplan.tfplan")); string(b) != "binary-plan\n" {
		t.Errorf("the saved workspace's plan = %q, %v", b, err)
	}
}

// TestUnitExecTerragruntApplies checks that a Terragrunt run's applying
// runs apply across the units, which plans again, records no outputs and
// reads none, and that a run with no changes saves nothing while
// planning; and that a destroy run destroys across the units, with the
// mounted tool when the specification downloads it.
func TestUnitExecTerragruntApplies(t *testing.T) {
	w := terragruntWorld(t)
	w.w.autodeploy = true
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
		"APPLYING succeeded 6",
	})
	trace := w.traced()
	equal(t, "applying", trace[len(trace)-1:], []string{
		"terragrunt run --all -- apply -input=false -lock-timeout=5m0s tf=terraform ni=true json=",
	})
	if got := strings.Join(w.w.taken, "\n"); strings.Contains(got, exec.OutputsArtifact) {
		t.Errorf("taken: %q", w.w.taken)
	}

	w = terragruntWorld(t)
	w.h.Spec.Tool.Download = true
	w.h.Spec.Run.Type = wire.PROPOSED
	w.w.then = map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: "DESTROYING"}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "DESTROYING succeeded 5",
	})
	equal(t, "trace", w.traced(), []string{
		"terragrunt run --all -- init -input=false -lock-timeout=5m0s tf=/bin/terraform ni=true " +
			"json=",
		"terragrunt run --all -- destroy -input=false -lock-timeout=5m0s tf=/bin/terraform " +
			"ni=true json=",
	})

	w = terragruntWorld(t)
	w.h.Spec.Run.Type = wire.PROPOSED
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if len(w.w.reports) != 3 || strings.Contains(strings.Join(w.w.taken, "\n"), "artifacts") {
		t.Errorf("a proposed run: %q, taken %q", w.events(), w.w.taken)
	}
}

// TestUnitExecTerragruntFails checks the notes of a Terragrunt run that
// fails: a unit that declares its own backend fails initialization naming
// it, before any command runs; a unit's plan that does not parse fails
// planning naming the unit; planning that writes no unit's plan fails;
// and a failed command names its key.
func TestUnitExecTerragruntFails(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(w *world)
		want []string
	}{
		"a unit's own backend": {
			edit: func(w *world) {
				name := filepath.Join(w.workspace, "a", "b", "terragrunt.hcl")
				if err := os.WriteFile(name, []byte("remote_state {\n}\n"), 0o600); err != nil {
					w.t.Fatal(err)
				}
			},
			want: []string{
				"INITIALIZING started 3", `INITIALIZING failed 4: The state backend cannot be ` +
					`configured: the unit "a/b" declares its own state backend: remote_state in ` +
					`terragrunt.hcl.`,
			},
		},
		"a unit's plan that does not parse": {
			edit: func(w *world) {
				name := filepath.Join(filepath.Dir(w.workspace), "unit-plans", "a", "b",
					"tfplan.json")
				if err := os.WriteFile(name, []byte("{"), 0o600); err != nil {
					w.t.Fatal(err)
				}
			},
			want: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				`PLANNING failed 5: The units' plans could not be read: the unit "a/b": `,
			},
		},
		"the root unit's plan that does not parse": {
			edit: func(w *world) {
				name := filepath.Join(filepath.Dir(w.workspace), "unit-plans", "tfplan.json")
				if err := os.WriteFile(name, []byte("{"), 0o600); err != nil {
					w.t.Fatal(err)
				}
			},
			want: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"PLANNING failed 5: The units' plans could not be read: the unit at the project " +
					"root: ",
			},
		},
		"a unit's plan that cannot be opened": {
			edit: func(w *world) {
				name := filepath.Join(filepath.Dir(w.workspace), "unit-plans", "tfplan.json")
				if err := os.Remove(name); err != nil {
					w.t.Fatal(err)
				}
				if err := os.Symlink("missing.json", name); err != nil {
					w.t.Fatal(err)
				}
			},
			want: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"PLANNING failed 5: The units' plans could not be read: openat tfplan.json: ",
			},
		},
		"no directory of plans": {
			edit: func(w *world) {
				plans := filepath.Join(filepath.Dir(w.workspace), "unit-plans")
				if err := os.RemoveAll(plans); err != nil {
					w.t.Fatal(err)
				}
			},
			want: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"PLANNING failed 5: The units' plans could not be read: no unit's plan was written",
			},
		},
		"no unit's plan": {
			edit: func(w *world) {
				plans := filepath.Join(filepath.Dir(w.workspace), "unit-plans")
				if err := os.RemoveAll(plans); err != nil {
					w.t.Fatal(err)
				}
				if err := os.Mkdir(plans, 0o755); err != nil {
					w.t.Fatal(err)
				}
			},
			want: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"PLANNING failed 5: The units' plans could not be read: no unit's plan was written",
			},
		},
		"a failed plan": {
			edit: func(w *world) { w.environ = append(w.environ, "EXIT_plan=3") },
			want: []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4",
				"PLANNING failed 5: The plan command failed with exit status 3.",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := terragruntWorld(t)
			c.edit(w)
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			got := w.events()
			if len(got) != len(c.want) || !strings.HasPrefix(got[len(got)-1], c.want[len(c.want)-1]) {
				t.Fatalf("reports:\n got %q\nwant %q", got, c.want)
			}
			equal(t, "reports", got[:len(got)-1], c.want[:len(c.want)-1])
		})
	}
}
