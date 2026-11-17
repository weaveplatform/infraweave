package runtimeconfig_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

func runtimeYAML(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"yaml": text})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestUnitRuntimeYAML(t *testing.T) {
	for _, text := range []string{"", "# empty\n", "---\n# empty\n", "{}"} {
		if in, err := runtimeconfig.Parse(runtimeYAML(t, text), "terraform"); err != nil || len(in.Hooks) != 0 {
			t.Errorf("empty YAML %q = %+v, %v", text, in, err)
		}
	}
	in, err := runtimeconfig.Parse(runtimeYAML(t, `
terraform_version: "~> 1.17"
opentofu_version: "1.13.9"
terraform_workflow_tool: CUSTOM
`), "terraform")
	if err != nil || in.TerraformVersion == nil || *in.TerraformVersion != "~> 1.17" ||
		in.OpenTofuVersion == nil || *in.OpenTofuVersion != "1.13.9" || in.WorkflowTool == nil || *in.WorkflowTool != "CUSTOM" {
		t.Fatalf("Terraform keys = %+v, %v", in, err)
	}
	for _, tc := range []struct{ vendor, text string }{
		{"opentofu", "opentofu: {version: '1.13.9', workflow_tool: CUSTOM}"},
		{"opentofu", "opentofu: {}"},
		{"terragrunt", "terragrunt: {terragrunt_tool: OPEN_TOFU, terragrunt_version: '1.2.2', terraform_version: '1.13.9', use_run_all: true, use_smart_sanitization: true, use_state_management: false}"},
		{"terragrunt", "terragrunt: {use_state_management: &mode true, use_run_all: *mode}\n...\n# tail"},
		{"terragrunt", "terragrunt: {}"},
	} {
		in, err := runtimeconfig.Parse(runtimeYAML(t, tc.text), tc.vendor)
		if err != nil {
			t.Fatalf("%s input = %v", tc.text, err)
		}
		again, err := runtimeconfig.Parse(runtimeYAML(t, in.RenderYAML()), tc.vendor)
		if err != nil || !reflect.DeepEqual(again, in) {
			t.Fatalf("vendor round trip = %+v, want %+v, %v", again, in, err)
		}
		public := in.Public()
		if tc.vendor == "opentofu" && public.OpenTofu == nil || tc.vendor == "terragrunt" && public.Terragrunt == nil {
			t.Errorf("vendor's public block absent: %+v", public)
		}
	}
}

func TestUnitRuntimeRefused(t *testing.T) {
	for _, tc := range []struct {
		name, text, vendor, pointer, key, reason string
	}{
		{"syntax", "before_plan: [", "terraform", "/runtimeConfig/yaml", "yaml", kernel.ReasonFormat},
		{"second document", "{}\n---\n{}", "terraform", "/runtimeConfig/yaml", "document", kernel.ReasonFormat},
		{"malformed tail", "{}\n---\n[", "terraform", "/runtimeConfig/yaml", "document", kernel.ReasonFormat},
		{"empty second document", "{}\n---\n", "terraform", "/runtimeConfig/yaml", "document", kernel.ReasonFormat},
		{"top scalar", "null", "terraform", "/runtimeConfig/yaml", "mapping", kernel.ReasonFormat},
		{"unknown", "befor_plan: []", "terraform", "/runtimeConfig/yaml", "befor_plan", kernel.ReasonUnknownField},
		{"hook scalar", "before_plan: x", "terraform", "/runtimeConfig/yaml", "before_plan", kernel.ReasonFormat},
		{"hook member", "before_plan: [3]", "terraform", "/runtimeConfig/yaml", "before_plan[0]", kernel.ReasonFormat},
		{"root type", "project_root: 3", "terraform", "/runtimeConfig/yaml", "project_root", kernel.ReasonFormat},
		{"root escape", "project_root: ../outside", "terraform", "/runtimeConfig/projectRoot", "project_root", kernel.ReasonRule},
		{"root unclean", "project_root: a/../b", "terraform", "/runtimeConfig/projectRoot", "project_root", kernel.ReasonRule},
		{"environment type", "environment: []", "terraform", "/runtimeConfig/yaml", "environment", kernel.ReasonFormat},
		{"variable type", "environment: {A: true}", "terraform", "/runtimeConfig/yaml", "environment.A", kernel.ReasonFormat},
		{"numeric key", "environment: {1: x}", "terraform", "/runtimeConfig/yaml", "keys", kernel.ReasonFormat},
		{"reserved prefix", "environment: {INFRAWEAVE_API_TOKEN: hidden}", "terraform", "/runtimeConfig/yaml", "environment", kernel.ReasonRule},
		{"reserved name", "environment: {TF_APPEND_USER_AGENT: hidden}", "terraform", "/runtimeConfig/yaml", "environment", kernel.ReasonRule},
		{"wrong vendor", "terragrunt: {}", "terraform", "/runtimeConfig/yaml", "terragrunt", kernel.ReasonRule},
		{"native wrong vendor", "opentofu: {}", "terragrunt", "/runtimeConfig/yaml", "opentofu", kernel.ReasonRule},
		{"native top workflow", "terraform_workflow_tool: TERRAFORM_FOSS", "opentofu", "/runtimeConfig/yaml", "terraform_workflow_tool", kernel.ReasonRule},
		{"native legacy workflow", "terraform_workflow_tool: OPEN_TOFU", "opentofu", "/runtimeConfig/yaml", "terraform_workflow_tool", kernel.ReasonRule},
		{"native top custom", "terraform_workflow_tool: CUSTOM", "opentofu", "/runtimeConfig/yaml", "terraform_workflow_tool", kernel.ReasonRule},
		{"block type", "terragrunt: null", "terragrunt", "/runtimeConfig/yaml", "terragrunt", kernel.ReasonFormat},
		{"block unknown", "terragrunt: {typo: true}", "terragrunt", "/runtimeConfig/yaml", "terragrunt.typo", kernel.ReasonUnknownField},
		{"native unknown", "opentofu: {concise: true}", "opentofu", "/runtimeConfig/yaml", "opentofu.concise", kernel.ReasonUnknownField},
		{"quoted bool", "terragrunt: {use_run_all: 'true'}", "terragrunt", "/runtimeConfig/yaml", "terragrunt.use_run_all", kernel.ReasonFormat},
		{"quoted yes", "terragrunt: {use_run_all: 'yes'}", "terragrunt", "/runtimeConfig/yaml", "terragrunt.use_run_all", kernel.ReasonFormat},
		{"numeric bool", "terragrunt: {use_run_all: 1}", "terragrunt", "/runtimeConfig/yaml", "terragrunt.use_run_all", kernel.ReasonFormat},
		{"legacy bool", "terragrunt: {use_run_all: yes}", "terragrunt", "/runtimeConfig/yaml", "terragrunt.use_run_all", kernel.ReasonFormat},
		{"invalid bool", "terragrunt: {use_run_all: !!bool invalid}", "terragrunt", "/runtimeConfig/yaml", "terragrunt.use_run_all", kernel.ReasonFormat},
		{"duplicate", "terragrunt: {use_run_all: true, use_run_all: false}", "terragrunt", "/runtimeConfig/yaml", "repeats", kernel.ReasonDuplicate},
		{"tool enum", "terragrunt: {terragrunt_tool: CUSTOM}", "terragrunt", "/runtimeConfig/yaml", "terragrunt.terragrunt_tool", kernel.ReasonEnum},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runtimeconfig.Parse(runtimeYAML(t, tc.text), tc.vendor)
			var refused *kernel.Invalid
			if !errors.As(err, &refused) || len(refused.Errors) != 1 || refused.Errors[0].Pointer != tc.pointer ||
				refused.Errors[0].Reason != tc.reason || !strings.Contains(refused.Errors[0].Message, tc.key) {
				t.Fatalf("refusal = %+v, %v", refused, err)
			}
		})
	}
	for _, raw := range []string{
		`{`, `null`, `{"yaml":"{}","beforePlan":[]}`, `{"yaml":3}`, `{"unknown":"value"}`, `{"unknown":null}`,
		`{"environment":[{"key":"A","value":"x"},{"key":"A","value":"y"}]}`,
		`{"environment":[{"key":"TF_VAR_infraweave_run_id","value":"x"}]}`,
		`{"beforePlan":"wrong type"}`, `{"projectRoot":"/outside"}`,
		`{"beforePlan":[null]}`, `{"gitSparseCheckoutPaths":[null]}`,
		`{"environment":[null]}`, `{"environment":[{"key":"TOKEN","value":null}]}`,
	} {
		if _, err := runtimeconfig.Parse(json.RawMessage(raw), "terraform"); err == nil {
			t.Errorf("invalid fields accepted: %s", raw)
		}
	}
}
