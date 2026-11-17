package runtimeconfig_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

func TestUnitRecord(t *testing.T) {
	for _, tc := range []struct {
		name, vendor, settings, tool string
	}{
		{"terraform", "terraform", `{"workspace":"production"}`, "terraform"},
		{"custom", "terraform", `{"workflowTool":"CUSTOM"}`, "terraform"},
		{"opentofu", "opentofu", `{"concise":true,"enableCPUProfiling":true,"externalStateAccessEnabled":true,"useSmartSanitization":true}`, "tofu"},
		{"custom opentofu", "opentofu", `{"workflowTool":"CUSTOM"}`, "tofu"},
		{"terragrunt", "terragrunt", `{"useSmartSanitization":true,"useStateManagement":true,"prefixResourceNamesWithModuleName":true}`, "tofu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, image := "live", "runner:fixed"
			spec := workersapi.Spec{
				Stack: workersapi.SpecStack{
					Vendor: tc.vendor, VendorConfig: json.RawMessage(tc.settings), ProjectRoot: &root,
					EnableWellKnownSecretMasking: true, EnableSensitiveOutputUpload: true,
				},
				RunnerImage: &image, Tool: &workersapi.SpecTool{Name: tc.tool, Version: "1.13.9", Download: true, CPUProfiling: true},
				Hooks:         map[string][]string{},
				Environment:   []workersapi.Variable{{Name: "SECRET", WriteOnly: true}},
				Files:         []workersapi.File{{Path: "private.txt", WriteOnly: true}},
				RuntimeConfig: json.RawMessage(`{"environment":[{"key":"SECRET","value":"do-not-record"}]}`),
				Source:        workersapi.SpecSource{SparsePaths: []string{"infra"}},
			}
			for _, phase := range []string{
				"after_apply", "after_destroy", "after_init", "after_perform", "after_plan", "after_run",
				"before_apply", "before_destroy", "before_init", "before_perform", "before_plan",
			} {
				spec.Hooks[phase] = []string{"echo " + phase}
			}
			if tc.vendor == "terragrunt" {
				spec.Terragrunt = &workersapi.SpecTerragrunt{Version: "1.2.2", Download: true, RunAll: true}
			}
			record, err := runtimeconfig.New(spec, true)
			if err != nil {
				t.Fatal(err)
			}
			var got workersapi.Spec
			record.Apply(&got)
			if !reflect.DeepEqual(got.Tool, spec.Tool) || !reflect.DeepEqual(got.Terragrunt, spec.Terragrunt) ||
				!reflect.DeepEqual(got.Hooks, spec.Hooks) || got.Stack.Vendor != tc.vendor ||
				string(got.Stack.VendorConfig) != tc.settings || *got.Stack.ProjectRoot != root ||
				*got.RunnerImage != image || !got.Stack.EnableWellKnownSecretMasking ||
				!got.Stack.EnableSensitiveOutputUpload || !record.Execution.ManagesState ||
				!reflect.DeepEqual(got.Source.SparsePaths, spec.Source.SparsePaths) {
				t.Fatalf("replayed: %+v", got)
			}
			data, err := json.Marshal(record)
			if err != nil || bytes.Contains(data, []byte("do-not-record")) || bytes.Contains(data, []byte("private.txt")) {
				t.Fatalf("record contains run values: %s, %v", data, err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal([]byte(record.Configuration.YAML), &doc); err != nil {
				t.Fatal(err)
			}
			if env, ok := doc["environment"].(map[string]any); !ok || len(env) != 1 || env["SECRET"] != "" ||
				strings.Contains(record.Configuration.YAML, "execution:") ||
				strings.Contains(record.Configuration.YAML, "vendorConfig:") {
				t.Fatalf("public YAML: %s", record.Configuration.YAML)
			}
			if _, err := runtimeconfig.Parse(runtimeYAML(t, record.Configuration.YAML), tc.vendor); err != nil {
				t.Fatalf("record YAML is not a supported runtime file: %v", err)
			}
			if tc.name == "opentofu" && (record.Configuration.OpenTofu == nil ||
				!record.Configuration.OpenTofu.EnableCPUProfiling || !record.Configuration.OpenTofu.Concise ||
				!record.Configuration.OpenTofu.ExternalStateAccessEnabled || !record.Configuration.OpenTofu.UseSmartSanitization ||
				strings.Contains(record.Configuration.YAML, "enable_cpu_profiling")) {
				t.Fatal("native flags were lost from JSON or added to the supported-key YAML")
			}
			got.Hooks["before_plan"][0] = "changed"
			got.Stack.VendorConfig[0] = '['
			got.Source.SparsePaths[0] = "changed"
			var again workersapi.Spec
			record.Apply(&again)
			if again.Hooks["before_plan"][0] != "echo before_plan" || string(again.Stack.VendorConfig) != tc.settings ||
				again.Source.SparsePaths[0] != "infra" {
				t.Fatalf("a specification mutated its record: %+v", again)
			}
		})
	}
}

func TestUnitRecordAbsentOptions(t *testing.T) {
	record, err := runtimeconfig.New(workersapi.Spec{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var out workersapi.Spec
	out.Tool, out.Terragrunt = &workersapi.SpecTool{}, &workersapi.SpecTerragrunt{}
	out.StateBackend = &workersapi.StateBackend{Path: "current"}
	image := "current"
	out.RunnerImage, out.Stack.ProjectRoot = &image, &image
	record.Apply(&out)
	if out.Tool != nil || out.Terragrunt != nil || out.RunnerImage != nil || out.Stack.ProjectRoot != nil ||
		out.StateBackend != nil || record.Execution.ManagesState {
		t.Fatalf("absent recorded options did not clear current options: %+v", out)
	}
	for phase, hooks := range out.Hooks {
		if hooks == nil || len(hooks) != 0 {
			t.Errorf("%s = %#v, want []", phase, hooks)
		}
	}
	if len(out.Hooks) != 11 || record.Configuration.Environment == nil || record.Configuration.GitSparseCheckoutPaths == nil {
		t.Fatalf("empty public lists: %+v", record.Configuration)
	}
	empty := ""
	explicit, err := runtimeconfig.New(workersapi.Spec{RunnerImage: &empty}, false)
	if err != nil {
		t.Fatal(err)
	}
	explicit.Apply(&out)
	if out.RunnerImage == nil || *out.RunnerImage != "" {
		t.Fatal("an explicitly empty image lost its presence")
	}
}

func TestUnitRecordRefusesMalformedSettings(t *testing.T) {
	if _, err := runtimeconfig.New(workersapi.Spec{Stack: workersapi.SpecStack{
		Vendor: "terraform", VendorConfig: json.RawMessage(`[`),
	}}, false); err == nil {
		t.Fatal("malformed vendor settings were recorded")
	}
}
