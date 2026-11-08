package terraform

import (
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/iac/internal/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Kind is the member of a stack input's vendorConfig that holds Terraform's
// settings.
const Kind = "terraform"

// WorkflowTools are the values of TerraformWorkflowTool.
var WorkflowTools = []string{"TERRAFORM_FOSS", "CUSTOM", "OPEN_TOFU"}

// ValidateSettings checks a stack's Terraform settings: the version
// constraint's syntax, the workflow tool, the workspace name and the types
// of the boolean switches. It refuses no field it does not know, which is
// the input schema's to refuse.
func ValidateSettings(s json.RawMessage) []kernel.FieldError {
	c := settings.New(s)
	c.Version("version")
	c.Enum("workflowTool", WorkflowTools...)
	c.Workspace("workspace")
	c.Bool("externalStateAccessEnabled")
	c.Bool("useSmartSanitization")
	return c.Errors()
}
