package workflow_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/iac/workflow"
)

// TestUnitWorkflowParse reads every key's template and the line it is
// on; a scalar of another type is its text as written.
func TestUnitWorkflowParse(t *testing.T) {
	data := "# a comment\ninit: true\nworkspaceSelect: tofu workspace select ${{ workspaceName }}\n" +
		"workspaceNew: 'tofu workspace new'\nplan: |\n  echo plan\n  tofu plan\n" +
		"showState: s\nshowPlan: 1\ngetOutputs: o\napply: a\ndestroy: d\n"
	got, lines, err := workflow.Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	want := workflow.Templates{
		workflow.Init:            "true",
		workflow.WorkspaceSelect: "tofu workspace select ${{ workspaceName }}",
		workflow.WorkspaceNew:    "tofu workspace new",
		workflow.Plan:            "echo plan\ntofu plan\n",
		workflow.ShowState:       "s",
		workflow.ShowPlan:        "1",
		workflow.GetOutputs:      "o",
		workflow.Apply:           "a",
		workflow.Destroy:         "d",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("templates %q", got)
	}
	if lines[workflow.Init] != 2 || lines[workflow.Plan] != 5 || lines[workflow.Destroy] != 12 {
		t.Errorf("lines %v", lines)
	}
}

// TestUnitWorkflowParseRefuses names every key at fault, with its line.
func TestUnitWorkflowParseRefuses(t *testing.T) {
	all := ""
	for _, k := range workflow.Keys {
		all += string(k) + ": x\n"
	}
	for name, c := range map[string]struct {
		data string
		want string
	}{
		"empty":         {data: "", want: "it is empty"},
		"only comments": {data: "# nothing\n", want: "it is empty"},
		"not YAML":      {data: "init: [\n", want: "did not find expected node content"},
		"a list":        {data: "- init\n", want: "line 1: it is not a mapping of keys to commands"},
		"over the size": {
			data: strings.Repeat("#", workflow.MaxSize+1),
			want: "it is 262145 bytes, over 262144",
		},
		"missing keys": {data: "init: x\n", want: "workspaceSelect, workspaceNew, plan, " +
			"showState, showPlan, getOutputs, apply, destroy missing"},
		"an unknown key": {data: all + "planx: y\n", want: `line 10: "planx" is not a key of a workflow`},
		"a key twice":    {data: all + "plan: y\n", want: "line 10: plan is given twice"},
		"a mapping": {
			data: strings.Replace(all, "plan: x", "plan: {a: b}", 1),
			want: "line 4: plan is not a string",
		},
		"null": {data: strings.Replace(all, "apply: x", "apply:", 1), want: "line 8: apply is empty"},
		"blank": {
			data: strings.Replace(all, "apply: x", "apply: '  '", 1),
			want: "line 8: apply is empty",
		},
		"several": {
			data: "init: [x]\nplan: ~\n",
			want: "line 1: init is not a string; line 2: plan is empty; workspaceSelect",
		},
	} {
		_, _, err := workflow.Parse([]byte(c.data))
		if !errors.Is(err, workflow.ErrFile) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %s", name, err, c.want)
		}
	}
}

// TestUnitWorkflowDefaults are the built-in templates under each binary,
// and a Terragrunt run's through run --all, and the variables are the run
// parameters, each with a schema.
func TestUnitWorkflowDefaults(t *testing.T) {
	for _, bin := range []string{"terraform", "tofu"} {
		d := workflow.Defaults(bin)
		if len(d) != len(workflow.Keys) {
			t.Errorf("%s: %d templates", bin, len(d))
		}
		for _, k := range workflow.Keys {
			if !strings.HasPrefix(d[k], bin+" ") {
				t.Errorf("%s %s: %s", bin, k, d[k])
			}
		}
	}
	for _, runAll := range []bool{false, true} {
		tg := workflow.Terragrunt(runAll)
		if len(tg) != len(workflow.Keys) {
			t.Errorf("terragrunt: %d templates", len(tg))
		}
		prefix := "terragrunt run -- "
		if runAll {
			prefix = "terragrunt run --all -- "
		}
		for _, k := range workflow.Keys {
			if !strings.HasPrefix(tg[k], prefix) {
				t.Errorf("terragrunt runAll=%t %s: %s", runAll, k, tg[k])
			}
		}
		for _, k := range []workflow.Key{workflow.Apply, workflow.Destroy} {
			if strings.Contains(tg[k], "-auto-approve") == runAll {
				t.Errorf("terragrunt runAll=%t %s approval: %s", runAll, k, tg[k])
			}
		}
	}
	values := workflow.Params{
		PlanFileName: "p", Lock: true, Targets: []string{"a"}, WorkspaceName: "w",
		LockTimeout: 90 * time.Second,
	}.Values()
	want := map[string]any{
		"planFileName": "p", "lock": true, "refresh": false, "targets": []any{"a"},
		"workspaceName": "w", "lockTimeout": "1m30s",
	}
	if !reflect.DeepEqual(values, want) {
		t.Errorf("values %v", values)
	}
	for _, v := range workflow.Variables {
		if _, ok := values[v.Name]; !ok || !json.Valid(v.Schema) {
			t.Errorf("variable %s", v.Name)
		}
	}
}
