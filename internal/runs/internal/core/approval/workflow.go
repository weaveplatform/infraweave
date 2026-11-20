package approval

import (
	"encoding/json"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func workflowTool(stack stacksapi.Settings) (policyapi.Fact[string], error) {
	var settings struct {
		WorkflowTool string `json:"workflowTool"`
		Tool         string `json:"tool"`
	}
	if len(stack.VendorConfig) > 0 && json.Unmarshal(stack.VendorConfig, &settings) != nil {
		return policyapi.Fact[string]{}, policyapi.ErrInput
	}
	switch stack.Vendor {
	case "terraform":
		if settings.WorkflowTool != "" {
			return known(settings.WorkflowTool), nil
		}
		return known("TERRAFORM_FOSS"), nil
	case "opentofu":
		if settings.WorkflowTool == "CUSTOM" {
			return known("CUSTOM"), nil
		}
		return known("OPEN_TOFU"), nil
	case "terragrunt":
		if settings.Tool != "" {
			return known(settings.Tool), nil
		}
		return known("TERRAFORM_FOSS"), nil
	}
	return policyapi.Fact[string]{}, nil
}
