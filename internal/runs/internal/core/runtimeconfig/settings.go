package runtimeconfig

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/iac/terragrunt"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ApplySettings applies the first handover's runtime settings to its local
// stack snapshot. Native vendor blocks replace the block as a whole; the
// stack's identity, authorization, source and backend choice stay with it.
func (in Input) ApplySettings(st *stacksapi.Settings) error {
	if err := in.validateVendor(st.Vendor); err != nil {
		return err
	}
	if in.ProjectRoot != nil {
		st.ProjectRoot = in.ProjectRoot
	}
	if in.RunnerImage != nil {
		st.RunnerImage = in.RunnerImage
	}
	if in.GitSparseCheckoutPaths != nil && len(*in.GitSparseCheckoutPaths) > 0 {
		st.GitSparseCheckoutPaths = slices.Clone(*in.GitSparseCheckoutPaths)
	}
	if st.Vendor == "opentofu" && in.OpenTofu == nil && (in.TerraformVersion != nil || in.OpenTofuVersion != nil) {
		return invalid(yamlPointer, kernel.ReasonRule,
			"A native OpenTofu runtime version is set in opentofu.version, not terraform_version or opentofu_version.")
	}
	if in.TerraformVersion != nil {
		st.TerraformVersion = in.TerraformVersion
	}
	if in.WorkflowTool != nil {
		var settings map[string]json.RawMessage
		if len(st.VendorConfig) > 0 {
			if err := json.Unmarshal(st.VendorConfig, &settings); err != nil {
				return fmt.Errorf("runs: read vendor settings for runtime workflow: %w", err)
			}
		}
		if settings == nil {
			settings = map[string]json.RawMessage{}
		}
		settings["workflowTool"], _ = json.Marshal(*in.WorkflowTool)
		st.VendorConfig, _ = json.Marshal(settings)
	}
	if t := in.OpenTofu; t != nil {
		workflow := "OPENTOFU"
		if t.WorkflowTool != nil {
			workflow = *t.WorkflowTool
		}
		version := t.Version
		if workflow == "CUSTOM" {
			version = nil
		}
		st.TerraformVersion = nil
		st.VendorConfig, _ = json.Marshal(struct {
			Version      *string `json:"version,omitempty"`
			WorkflowTool string  `json:"workflowTool"`
		}{version, workflow})
	}
	if t := in.Terragrunt; t != nil {
		tool := terragrunt.DefaultTool
		if t.Tool != nil {
			tool = *t.Tool
		}
		st.TerraformVersion = nil
		st.VendorConfig, _ = json.Marshal(terragrunt.Settings{
			Tool: &tool, TerragruntVersion: t.Version, TerraformVersion: t.TerraformVersion,
			UseRunAll: t.UseRunAll, UseSmartSanitization: t.UseSmartSanitization,
			UseStateManagement: &t.UseStateManagement,
		})
	}
	if st.Vendor == "terraform" && in.OpenTofuVersion != nil && DrivenVendor(*st) == "opentofu" {
		st.TerraformVersion = in.OpenTofuVersion
	}
	return nil
}

func (in Input) validateVendor(vendor string) error {
	if in.OpenTofu != nil && vendor != "opentofu" {
		return invalid(yamlPointer, kernel.ReasonRule, "opentofu has no effect on this stack's vendor.")
	}
	if in.Terragrunt != nil && vendor != "terragrunt" {
		return invalid(yamlPointer, kernel.ReasonRule, "terragrunt has no effect on this stack's vendor.")
	}
	if in.WorkflowTool != nil && vendor != "terraform" {
		return invalid(yamlPointer, kernel.ReasonRule,
			"terraform_workflow_tool applies to Terraform stacks; use the stack vendor's own runtime block.")
	}
	return nil
}

// DrivenVendor is the tool a stack's resolved settings select. A Terraform
// stack's legacy OpenTofu workflow uses OpenTofu's catalogue and binary too.
func DrivenVendor(st stacksapi.Settings) string {
	if st.Vendor == terragrunt.Kind {
		return terragrunt.Read(st.VendorConfig).Driven()
	}
	if st.Vendor == "terraform" {
		var settings struct {
			WorkflowTool string `json:"workflowTool"`
		}
		if json.Unmarshal(st.VendorConfig, &settings) == nil && settings.WorkflowTool == "OPEN_TOFU" {
			return "opentofu"
		}
	}
	return st.Vendor
}

// OwnsToolVersion reports whether the run's input chose the constraint or
// replaced the block that defaults it, so a preparation refusal names it.
func (in Input) OwnsToolVersion() bool {
	return in.TerraformVersion != nil || in.OpenTofuVersion != nil || in.OpenTofu != nil || in.Terragrunt != nil
}
