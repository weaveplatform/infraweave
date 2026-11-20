package terragrunt

import (
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/iac/internal/settings"
	"github.com/weaveplatform/infraweave/internal/iac/opentofu"
	"github.com/weaveplatform/infraweave/internal/iac/terraform"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Kind is the member of a stack input's vendorConfig that holds
// Terragrunt's settings.
const Kind = "terragrunt"

// The values of TerragruntTool, the tool Terragrunt drives.
const (
	ToolTerraform = "TERRAFORM_FOSS"
	ToolOpenTofu  = "OPEN_TOFU"
	ToolManual    = "MANUALLY_PROVISIONED"
)

// Tools are the values of TerragruntTool.
var Tools = []string{ToolTerraform, ToolOpenTofu, ToolManual}

// DefaultTool is the tool of settings that name none, as the API's input
// defaults it.
const DefaultTool = ToolTerraform

// Settings are the Terragrunt settings a stack's vendorConfig holds that
// the server reads.
type Settings struct {
	Tool                              *string `json:"tool"`
	TerragruntVersion                 *string `json:"terragruntVersion"`
	TerraformVersion                  *string `json:"terraformVersion"`
	UseRunAll                         bool    `json:"useRunAll"`
	UseSmartSanitization              bool    `json:"useSmartSanitization"`
	UseStateManagement                *bool   `json:"useStateManagement"`
	SkipReplan                        bool    `json:"skipReplan"`
	PrefixResourceNamesWithModuleName *bool   `json:"prefixResourceNamesWithModuleName"`
}

// Read reads s, a stack's Terragrunt settings. The stack's write checked
// them, so settings that do not decode read as the zero Settings.
func Read(s json.RawMessage) Settings {
	var out Settings
	if json.Unmarshal(s, &out) != nil {
		return Settings{}
	}
	return out
}

// ToolName is the tool the settings name, DefaultTool when they name
// none.
func (s Settings) ToolName() string {
	if s.Tool == nil {
		return DefaultTool
	}
	return *s.Tool
}

// Driven is the vendor kind of the tool Terragrunt drives, whose release
// a run mounts beside Terragrunt's: terraform for TERRAFORM_FOSS and
// opentofu for OPEN_TOFU. It is empty for any other tool, which no
// stack's write accepts.
func (s Settings) Driven() string {
	switch s.ToolName() {
	case ToolTerraform:
		return terraform.Kind
	case ToolOpenTofu:
		return opentofu.Kind
	}
	return ""
}

// ManagesState reports whether a stack created with the Terragrunt
// settings s and manageState keeps its state in infraweave's backend:
// useStateManagement decides when s sets it, and manageState when it is
// null, as the Terraform provider documents its precedence.
//
// ref: decision 0010 § "How a run reaches it"
func ManagesState(s json.RawMessage, manageState bool) bool {
	if u := Read(s).UseStateManagement; u != nil {
		return *u
	}
	return manageState
}

// later are the switches whose true asks for what infraweave does not run
// yet, each with the message that refuses it: Terragrunt apply replans
// and keeps no changes or outputs to prefix.
var later = []struct{ name, message string }{
	{
		"skipReplan",
		"A Terragrunt stack's apply plans again, and reusing the saved plan is not supported yet; " +
			"skipReplan is false.",
	},
	{
		"skipReplanWhenRunAll",
		"A Terragrunt stack's apply plans again, and reusing the saved plan is not supported yet; " +
			"skipReplanWhenRunAll is false.",
	},
	{
		"prefixResourceNamesWithModuleName",
		"A Terragrunt run keeps no resources or outputs to prefix yet; " +
			"prefixResourceNamesWithModuleName is false or null.",
	},
}

// ValidateSettings checks a stack's Terragrunt settings: the syntax of
// both version constraints, the tool and the types of the switches. It
// refuses the manually provisioned tool and a true value of each switch
// in later, which ask for what infraweave does not run yet. It refuses
// no field it does not know, which is the input schema's to refuse.
//
// ref: decision 0008 § "The run specification and its secrets"
func ValidateSettings(s json.RawMessage) []kernel.FieldError {
	c := settings.New(s)
	c.Version("terragruntVersion")
	c.Version("terraformVersion")
	c.Enum("tool", Tools...)
	c.Unsupported("tool", ToolManual, "A Terragrunt stack's tool is OPEN_TOFU or TERRAFORM_FOSS; "+
		"a manually provisioned tool is not supported yet.")
	c.Bool("useSmartSanitization")
	c.Bool("useRunAll")
	c.Bool("useStateManagement")
	for _, l := range later {
		c.Bool(l.name)
		c.Unsupported(l.name, true, l.message)
	}
	return c.Errors()
}
