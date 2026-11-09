package opentofu

import (
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/iac/internal/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Kind is the member of a stack input's vendorConfig that holds OpenTofu's
// settings.
const Kind = "opentofu"

// WorkflowTools are the values of OpenTofuWorkflowTool.
var WorkflowTools = []string{"OPENTOFU", "CUSTOM"}

// ValidateSettings checks a stack's OpenTofu settings: the version
// constraint's syntax, the workflow tool, the workspace name and the types
// of the boolean switches. Whether concise or enableCPUProfiling suits the
// release the constraint resolves to is decided with that release, not
// here. It refuses no field it does not know, which is the input schema's
// to refuse.
func ValidateSettings(s json.RawMessage) []kernel.FieldError {
	c := settings.New(s)
	c.Version("version")
	c.Enum("workflowTool", WorkflowTools...)
	c.Workspace("workspace")
	c.Bool("externalStateAccessEnabled")
	c.Bool("useSmartSanitization")
	c.Bool("concise")
	c.Bool("enableCPUProfiling")
	return c.Errors()
}
