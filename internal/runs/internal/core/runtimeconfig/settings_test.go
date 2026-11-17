package runtimeconfig

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/terragrunt"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestUnitRuntimeSettings(t *testing.T) {
	root, image, version, workflow := "infra", "runner:runtime", "1.6.6", "OPEN_TOFU"
	paths := []string{"infra"}
	st := stacksapi.Settings{
		Vendor: "terraform", ProjectRoot: new("stack"), RunnerImage: new("runner:stack"),
		TerraformVersion: new("1.17.2"), GitSparseCheckoutPaths: []string{"stack"},
		VendorConfig: json.RawMessage(`{"workspace":"kept","workflowTool":"TERRAFORM_FOSS"}`),
		ManagesState: true,
	}
	in := Input{
		ProjectRoot: &root, RunnerImage: &image, TerraformVersion: new("ignored"),
		OpenTofuVersion: &version, WorkflowTool: &workflow, GitSparseCheckoutPaths: &paths,
	}
	if err := in.ApplySettings(&st); err != nil || *st.ProjectRoot != root || *st.RunnerImage != image ||
		*st.TerraformVersion != version || !slices.Equal(st.GitSparseCheckoutPaths, paths) ||
		DrivenVendor(st) != "opentofu" || !st.ManagesState {
		t.Fatalf("runtime settings: %+v, %v", st, err)
	}
	var vendor map[string]any
	if err := json.Unmarshal(st.VendorConfig, &vendor); err != nil || vendor["workspace"] != "kept" ||
		vendor["workflowTool"] != workflow {
		t.Fatalf("top-level workflow preserves other settings: %s, %v", st.VendorConfig, err)
	}
	empty := []string{}
	if err := (Input{GitSparseCheckoutPaths: &empty}).ApplySettings(&st); err != nil ||
		!slices.Equal(st.GitSparseCheckoutPaths, paths) {
		t.Fatalf("empty paths use stack paths: %+v, %v", st, err)
	}
	if (Input{}).OwnsToolVersion() || !(Input{TerraformVersion: &version}).OwnsToolVersion() ||
		!(Input{OpenTofuVersion: &version}).OwnsToolVersion() ||
		!(Input{OpenTofu: &OpenTofuInput{}}).OwnsToolVersion() ||
		!(Input{Terragrunt: &TerragruntInput{}}).OwnsToolVersion() {
		t.Fatal("runtime version ownership")
	}
}

func TestUnitRuntimeVendorBlocks(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "custom"}[custom], func(t *testing.T) {
			st := stacksapi.Settings{
				Vendor: "opentofu", TerraformVersion: new("old"),
				VendorConfig: json.RawMessage(`{"version":"old","concise":true,"enableCPUProfiling":true}`),
			}
			block := &OpenTofuInput{Version: new("1.6.3")}
			if custom {
				block.WorkflowTool = new("CUSTOM")
			}
			if err := (Input{OpenTofu: block}).ApplySettings(&st); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(st.VendorConfig, &got); err != nil || got["concise"] != nil ||
				got["enableCPUProfiling"] != nil || st.TerraformVersion != nil || DrivenVendor(st) != "opentofu" {
				t.Fatalf("native block replaces all settings: %s, %v", st.VendorConfig, err)
			}
			if custom && (got["workflowTool"] != "CUSTOM" || got["version"] != nil) ||
				!custom && (got["workflowTool"] != "OPENTOFU" || got["version"] != "1.6.3") {
				t.Fatalf("native defaults and custom version: %s", st.VendorConfig)
			}
		})
	}
	st := stacksapi.Settings{
		Vendor: "terragrunt", ManagesState: true,
		VendorConfig: json.RawMessage(`{"useRunAll":true,"useSmartSanitization":true,"tool":"OPEN_TOFU"}`),
	}
	if err := (Input{Terragrunt: &TerragruntInput{}}).ApplySettings(&st); err != nil {
		t.Fatal(err)
	}
	got := terragrunt.Read(st.VendorConfig)
	if got.UseRunAll || got.UseSmartSanitization || got.UseStateManagement == nil || *got.UseStateManagement ||
		got.Tool == nil || *got.Tool != "TERRAFORM_FOSS" || !st.ManagesState || DrivenVendor(st) != "terraform" {
		t.Fatalf("whole Terragrunt block defaults without changing backend: %+v, %s", st, st.VendorConfig)
	}
	st.ManagesState = false
	if err := (Input{Terragrunt: &TerragruntInput{Tool: new("OPEN_TOFU"), UseStateManagement: true}}).
		ApplySettings(&st); err != nil || st.ManagesState || DrivenVendor(st) != "opentofu" {
		t.Fatalf("runtime state flag cannot enable backend: %+v, %v", st, err)
	}
}

func TestUnitRuntimeSettingsRefused(t *testing.T) {
	for name, in := range map[string]Input{
		"top-level Terraform version on native OpenTofu": {TerraformVersion: new("1.6.3")},
		"top-level OpenTofu version on native OpenTofu":  {OpenTofuVersion: new("1.6.3")},
		"Terraform workflow on native OpenTofu":          {WorkflowTool: new("TERRAFORM_FOSS")},
		"legacy OpenTofu workflow on native OpenTofu":    {WorkflowTool: new("OPEN_TOFU")},
		"Terragrunt block on native OpenTofu":            {Terragrunt: &TerragruntInput{}},
	} {
		t.Run(name, func(t *testing.T) {
			st := stacksapi.Settings{Vendor: "opentofu"}
			err := in.ApplySettings(&st)
			inv, ok := errors.AsType[*kernel.Invalid](err)
			if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/runtimeConfig/yaml" ||
				inv.Errors[0].Reason != kernel.ReasonRule || st.VendorConfig != nil {
				t.Fatalf("native refusal cannot create invalid vendor workflow: %+v, %v", st, err)
			}
		})
	}
	st := stacksapi.Settings{Vendor: "terraform", VendorConfig: json.RawMessage(`broken`)}
	if err := (Input{WorkflowTool: new("CUSTOM")}).ApplySettings(&st); err == nil {
		t.Fatal("corrupt stored settings accepted")
	}
	if DrivenVendor(st) != "terraform" {
		t.Fatal("corrupt settings changed vendor")
	}
}
