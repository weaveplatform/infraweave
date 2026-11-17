package runtimeconfig

import (
	"encoding/json"
	"reflect"
	"testing"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestUnitFileSettingsPrecedence(t *testing.T) {
	root, image, version, workflow := "default-root", "default-image", "1.6.6", "CUSTOM"
	paths := []string{"defaults"}
	defaults := Input{
		Hooks:       map[string][]string{"before_plan": {"defaults"}, "after_plan": {"defaults-after"}},
		ProjectRoot: &root, RunnerImage: &image, TerraformVersion: &version,
		WorkflowTool: &workflow, GitSparseCheckoutPaths: &paths,
	}
	st := stacksapi.Settings{
		ProjectRoot: new("stack-root"), RunnerImage: new("stack-image"),
		TerraformVersion: new("stack-version"), GitSparseCheckoutPaths: []string{"stack"},
		Hooks: map[string][]string{"before_plan": {"stack"}}, VendorConfig: json.RawMessage(`{"workflowTool":"TERRAFORM_FOSS"}`),
	}
	for _, v := range []string{"1", "2"} {
		f := File{Version: v, Defaults: defaults}
		got := f.Settings(st)
		if v == "1" {
			if got.ProjectRoot != nil || got.RunnerImage != nil || got.TerraformVersion != nil ||
				got.WorkflowTool != nil || got.GitSparseCheckoutPaths != nil || got.Hooks["before_plan"] != nil {
				t.Fatalf("v1 replaced observable stack settings: %+v", got)
			}
		} else if got.ProjectRoot != &root || !reflect.DeepEqual(got.Hooks["before_plan"], []string{"defaults"}) {
			t.Fatalf("v2 did not apply defaults: %+v", got)
		}
		if !reflect.DeepEqual(got.Hooks["after_plan"], []string{"defaults-after"}) {
			t.Fatal("an unstored own phase incorrectly blocked defaults")
		}
		entryRoot := ""
		emptyPaths := []string{}
		f.Entry = &Input{ProjectRoot: &entryRoot, Hooks: map[string][]string{"before_plan": {}}, GitSparseCheckoutPaths: &emptyPaths}
		got = f.Settings(st)
		if got.ProjectRoot == nil || *got.ProjectRoot != "" || got.GitSparseCheckoutPaths == nil ||
			len(*got.GitSparseCheckoutPaths) != 0 || got.Hooks["before_plan"] == nil || len(got.Hooks["before_plan"]) != 0 {
			t.Fatalf("explicit empty entry lost presence in v%s: %+v", v, got)
		}
		custom := Overlay(got, Input{ProjectRoot: new("trigger"), Hooks: map[string][]string{"before_plan": {"trigger"}}})
		if *custom.ProjectRoot != "trigger" || custom.Hooks["before_plan"][0] != "trigger" ||
			!reflect.DeepEqual(defaults.Hooks["before_plan"], []string{"defaults"}) {
			t.Fatalf("trigger overlay mutated the file: %+v", custom)
		}
	}
}

func TestUnitFileEnvironmentPrecedence(t *testing.T) {
	elements := []configapi.Element{
		{Name: "LEVEL", Source: configapi.SourceStack},
		{Name: "CONTEXT", Source: configapi.SourceContext},
		{Name: "FILE", Source: configapi.SourceStack, FileMount: true},
	}
	defaults := []Variable{{Key: "LEVEL", Value: "defaults"}, {Key: "CONTEXT", Value: "defaults"}, {Key: "FILE", Value: "variable"}}
	for _, v := range []string{"1", "2"} {
		f := File{Version: v, Defaults: Input{Environment: defaults}}
		want := defaults
		if v == "1" {
			want = defaults[1:]
		}
		if got := f.Variables(elements); !reflect.DeepEqual(got, want) {
			t.Fatalf("v%s env: %+v, want %+v", v, got, want)
		}
		f.Entry = &Input{Environment: []Variable{{Key: "EXTRA", Value: "entry"}}}
		if got := f.Variables(elements); !reflect.DeepEqual(got, f.Entry.Environment) {
			t.Fatalf("entry merged default map: %+v", got)
		}
		f.Entry.Environment = []Variable{}
		if got := f.Variables(elements); got == nil || len(got) != 0 {
			t.Fatalf("empty entry failed to clear defaults: %+v", got)
		}
	}
}

func TestUnitFileVendorBlockReplacement(t *testing.T) {
	f := File{Version: "2", Defaults: Input{OpenTofu: &OpenTofuInput{Version: new("old")}, Terragrunt: &TerragruntInput{UseRunAll: true}}}
	f.Entry = &Input{OpenTofu: &OpenTofuInput{}, Terragrunt: &TerragruntInput{}}
	got := f.Settings(stacksapi.Settings{})
	if got.OpenTofu.Version != nil || got.Terragrunt.UseRunAll {
		t.Fatalf("blocks merged with defaults: %+v", got)
	}
	f.Version = "1"
	f.Entry = nil
	got = f.Settings(stacksapi.Settings{VendorConfig: json.RawMessage(`{"version":"own"}`)})
	if got.OpenTofu != nil || got.Terragrunt != nil {
		t.Fatalf("v1 defaults beat explicit vendor settings: %+v", got)
	}
}
