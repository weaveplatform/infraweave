package opentofu_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/iactest"
)

func TestUnitContract(t *testing.T) {
	v, ok := iac.Lookup(iac.OpenTofu)
	if !ok {
		t.Fatal("opentofu is not registered")
	}
	plans, err := iactest.Recorded("../plan/testdata/tofu")
	if err != nil {
		t.Fatal(err)
	}
	iactest.Run(t, v, []iactest.Case{
		{Name: "every setting", Settings: `{"version": "1.8.3", "workflowTool": "OPENTOFU",
			"workspace": "default", "externalStateAccessEnabled": false, "useSmartSanitization": true,
			"concise": true, "enableCPUProfiling": false}`},
		{Name: "a range", Settings: `{"version": "~> 1.8, != 1.8.1"}`},
		{Name: "a custom tool", Settings: `{"workflowTool": "CUSTOM"}`},
		{
			Name:     "a version that does not parse",
			Settings: `{"version": "latest"}`,
			Refused:  []string{"/version"},
		},
		{
			Name: "Terraform's tool name", Settings: `{"workflowTool": "TERRAFORM_FOSS"}`,
			Refused: []string{"/workflowTool"},
		},
		{
			Name:     "a tool that is a number",
			Settings: `{"workflowTool": 3}`,
			Refused:  []string{"/workflowTool"},
		},
		{
			Name:     "a workspace with a percent",
			Settings: `{"workspace": "a%b"}`,
			Refused:  []string{"/workspace"},
		},
		{
			Name:     "switches that are strings",
			Settings: `{"concise": "on", "enableCPUProfiling": "off"}`,
			Refused:  []string{"/concise", "/enableCPUProfiling"},
		},
	}, plans...)
}
