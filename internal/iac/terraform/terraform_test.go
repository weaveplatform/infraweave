package terraform_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/iactest"
)

func TestUnitContract(t *testing.T) {
	v, ok := iac.Lookup(iac.Terraform)
	if !ok {
		t.Fatal("terraform is not registered")
	}
	iactest.Run(t, v, []iactest.Case{
		{Name: "every setting", Settings: `{"version": "~> 1.5.0", "workflowTool": "TERRAFORM_FOSS",
			"workspace": "prod-eu", "externalStateAccessEnabled": true, "useSmartSanitization": false}`},
		{Name: "every setting null", Settings: `{"version": null, "workflowTool": null,
			"workspace": null, "externalStateAccessEnabled": null, "useSmartSanitization": null}`},
		{Name: "a range", Settings: `{"version": ">= 1.5, < 2.0"}`},
		{Name: "a custom tool", Settings: `{"workflowTool": "CUSTOM"}`},
		{Name: "a field the schema refuses", Settings: `{"concise": "yes"}`},
		{
			Name:     "a version that does not parse",
			Settings: `{"version": "1.x"}`,
			Refused:  []string{"/version"},
		},
		{Name: "an empty version", Settings: `{"version": ""}`, Refused: []string{"/version"}},
		{
			Name:     "a version that is a number",
			Settings: `{"version": 1.5}`,
			Refused:  []string{"/version"},
		},
		{
			Name:     "OpenTofu's tool name",
			Settings: `{"workflowTool": "OPENTOFU"}`,
			Refused:  []string{"/workflowTool"},
		},
		{
			Name:     "a workspace with a slash",
			Settings: `{"workspace": "prod/eu"}`,
			Refused:  []string{"/workspace"},
		},
		{
			Name:     "a workspace with a space",
			Settings: `{"workspace": "prod eu"}`,
			Refused:  []string{"/workspace"},
		},
		{
			Name:     "switches that are strings",
			Settings: `{"externalStateAccessEnabled": "true", "useSmartSanitization": 1}`,
			Refused:  []string{"/externalStateAccessEnabled", "/useSmartSanitization"},
		},
	})
}
