package policyinput

import (
	"encoding/json"
	"errors"
	"testing"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestUnitWorkflowUsesStoredToolAndVendorDefault(t *testing.T) {
	for _, tc := range []struct{ vendor, body, want string }{
		{"terraform", "{}", "TERRAFORM_FOSS"},
		{"terraform", `{"workflowTool":"CUSTOM"}`, "CUSTOM"},
		{"terraform", `{"workflowTool":"OPEN_TOFU"}`, "OPEN_TOFU"},
		{"opentofu", "{}", "OPEN_TOFU"},
		{"opentofu", `{"workflowTool":"OPENTOFU"}`, "OPEN_TOFU"},
		{"opentofu", `{"workflowTool":"CUSTOM"}`, "CUSTOM"},
		{"terragrunt", "{}", "TERRAFORM_FOSS"},
		{"terragrunt", `{"tool":"OPEN_TOFU"}`, "OPEN_TOFU"},
		{"pulumi", "{}", ""},
	} {
		t.Run(tc.vendor+tc.body, func(t *testing.T) {
			got, err := workflowTool(stacksapi.Settings{Vendor: tc.vendor, VendorConfig: json.RawMessage(tc.body)})
			if err != nil || tc.want != "" && (!got.Known || got.Value == nil || *got.Value != tc.want) || tc.want == "" && (got.Known || got.Value != nil) {
				t.Fatalf("policy tool fact=%+v %v want %q", got, err, tc.want)
			}
		})
	}
	if _, err := workflowTool(stacksapi.Settings{Vendor: "terraform", VendorConfig: json.RawMessage(`{"workflowTool":false}`)}); !errors.Is(err, policyapi.ErrInput) {
		t.Fatalf("invalid stored workflow became a fact: %v", err)
	}
}
