package terragrunt_test

import (
	"encoding/json"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/iactest"
	"github.com/weaveplatform/infraweave/internal/iac/terragrunt"
)

// TestUnitContract holds Terragrunt to the vendor contract over a table of
// settings it accepts and refuses. Each unit's plan is its tool's, so the
// recorded OpenTofu plans are Terragrunt's too.
func TestUnitContract(t *testing.T) {
	v, ok := iac.Lookup(iac.Terragrunt)
	if !ok {
		t.Fatal("terragrunt is not registered")
	}
	plans, err := iactest.Recorded("../plan/testdata/tofu")
	if err != nil {
		t.Fatal(err)
	}
	iactest.Run(t, v, []iactest.Case{
		{Name: "every setting it runs", Settings: `{"tool": "OPEN_TOFU", "terragruntVersion": "~> 0.93",
			"terraformVersion": ">= 1.8, < 2.0", "useRunAll": true, "useSmartSanitization": false,
			"useStateManagement": false, "skipReplan": false, "skipReplanWhenRunAll": false,
			"prefixResourceNamesWithModuleName": false}`},
		{Name: "every setting null", Settings: `{"tool": null, "terragruntVersion": null,
			"terraformVersion": null, "useStateManagement": null, "skipReplan": null,
			"skipReplanWhenRunAll": null, "prefixResourceNamesWithModuleName": null}`},
		{Name: "Terraform", Settings: `{"tool": "TERRAFORM_FOSS", "useRunAll": false,
			"useSmartSanitization": false, "useStateManagement": true}`},
		{Name: "a field the schema refuses", Settings: `{"workspace": "prod/eu"}`},
		{
			Name:     "the manually provisioned tool",
			Settings: `{"tool": "MANUALLY_PROVISIONED"}`,
			Refused:  []string{"/tool"},
		},
		{Name: "a tool no enum holds", Settings: `{"tool": "OPENTOFU"}`, Refused: []string{"/tool"}},
		{Name: "a tool that is a number", Settings: `{"tool": 1}`, Refused: []string{"/tool"}},
		{
			Name:     "smart sanitization",
			Settings: `{"useSmartSanitization": true}`,
			Refused:  []string{"/useSmartSanitization"},
		},
		{
			Name:     "reusing the saved plan",
			Settings: `{"skipReplan": true, "skipReplanWhenRunAll": true}`,
			Refused:  []string{"/skipReplan", "/skipReplanWhenRunAll"},
		},
		{
			Name:     "prefixed resource names",
			Settings: `{"prefixResourceNamesWithModuleName": true}`,
			Refused:  []string{"/prefixResourceNamesWithModuleName"},
		},
		{
			Name:     "versions that do not parse",
			Settings: `{"terragruntVersion": "latest", "terraformVersion": "1.x"}`,
			Refused:  []string{"/terragruntVersion", "/terraformVersion"},
		},
		{
			Name: "switches that are strings",
			Settings: `{"useRunAll": "yes", "useStateManagement": "no",
				"useSmartSanitization": "true", "skipReplan": "true"}`,
			Refused: []string{
				"/useRunAll", "/useStateManagement", "/useSmartSanitization",
				"/skipReplan",
			},
		},
	}, plans...)
}

// TestUnitSettings reads the tool a stack's Terragrunt drives, the
// default's when it names none, and settings that do not decode as none.
func TestUnitSettings(t *testing.T) {
	for _, c := range []struct {
		settings, tool, driven string
	}{
		{`{}`, terragrunt.ToolTerraform, "terraform"},
		{`{"tool": null}`, terragrunt.ToolTerraform, "terraform"},
		{`{"tool": "TERRAFORM_FOSS"}`, terragrunt.ToolTerraform, "terraform"},
		{`{"tool": "OPEN_TOFU"}`, terragrunt.ToolOpenTofu, "opentofu"},
		{`{"tool": "MANUALLY_PROVISIONED"}`, terragrunt.ToolManual, ""},
		{`["OPEN_TOFU"]`, terragrunt.ToolTerraform, "terraform"},
	} {
		s := terragrunt.Read(json.RawMessage(c.settings))
		if s.ToolName() != c.tool || s.Driven() != c.driven {
			t.Errorf("%s: tool %s driving %q, want %s driving %q", c.settings, s.ToolName(),
				s.Driven(), c.tool, c.driven)
		}
	}
	for settings, want := range map[string]bool{
		`{"useStateManagement": false}`: false, `{"useStateManagement": null}`: true, `{}`: true,
	} {
		if got := terragrunt.ManagesState(json.RawMessage(settings), true); got != want {
			t.Errorf("%s with manageState: %t, want %t", settings, got, want)
		}
	}
	s := terragrunt.Read(json.RawMessage(`{"terragruntVersion": "0.93.3", "useRunAll": true,
		"useStateManagement": false, "prefixResourceNamesWithModuleName": false}`))
	if *s.TerragruntVersion != "0.93.3" || !s.UseRunAll || *s.UseStateManagement ||
		*s.PrefixResourceNamesWithModuleName || s.TerraformVersion != nil {
		t.Errorf("Read = %+v", s)
	}
}
