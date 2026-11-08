package iac_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitValidateSettingsAccepts(t *testing.T) {
	version := "~> 1.5"
	for name, in := range map[string]iac.Input{
		"nothing":   {},
		"null":      {VendorConfig: json.RawMessage(`null`)},
		"no member": {VendorConfig: json.RawMessage(`{}`)},
		"terraform": {VendorConfig: json.RawMessage(`{"terraform": {"version": "1.5.7"}}`)},
		"opentofu, the rest null": {VendorConfig: json.RawMessage(
			`{"terraform": null, "opentofu": {"workflowTool": "OPENTOFU"}, "pulumi": null}`)},
		"terraformVersion": {TerraformVersion: &version},
	} {
		if err := iac.ValidateSettings(in); err != nil {
			t.Errorf("%s: ValidateSettings = %v, want nil", name, err)
		}
	}
}

func TestUnitValidateSettingsRefuses(t *testing.T) {
	bad := "2.x"
	for name, c := range map[string]struct {
		in   iac.Input
		want []kernel.FieldError
	}{
		"two members": {
			in: iac.Input{VendorConfig: json.RawMessage(`{"terraform": {}, "opentofu": {}}`)},
			want: []kernel.FieldError{{
				Pointer: "/vendorConfig", Reason: kernel.ReasonRule,
				Message: "A stack has one vendor, and vendorConfig sets opentofu and terraform.",
			}},
		},
		"a vendor infraweave does not run": {
			in: iac.Input{VendorConfig: json.RawMessage(`{"pulumi": {"loginURL": "x"}}`)},
			want: []kernel.FieldError{{
				Pointer: "/vendorConfig/pulumi", Reason: kernel.ReasonRule,
				Message: "infraweave runs no pulumi stacks.",
			}},
		},
		"a member name a pointer escapes": {
			in: iac.Input{VendorConfig: json.RawMessage(`{"a/b~c": {}}`)},
			want: []kernel.FieldError{{
				Pointer: "/vendorConfig/a~1b~0c", Reason: kernel.ReasonRule,
				Message: "infraweave runs no a/b~c stacks.",
			}},
		},
		"not an object": {
			in: iac.Input{VendorConfig: json.RawMessage(`["terraform"]`)},
			want: []kernel.FieldError{{
				Pointer: "/vendorConfig", Reason: kernel.ReasonType,
				Message: "The vendorConfig field is an object or null.",
			}},
		},
		"a setting its vendor refuses, and terraformVersion": {
			in: iac.Input{
				VendorConfig:     json.RawMessage(`{"terraform": {"workflowTool": "OPENTOFU"}}`),
				TerraformVersion: &bad,
			},
			want: []kernel.FieldError{
				{
					Pointer: "/terraformVersion", Reason: kernel.ReasonFormat,
					Message: `The terraformVersion field is not a version constraint: "2.x" is not a version such as 1.6.0.`,
				},
				{
					Pointer: "/vendorConfig/terraform/workflowTool", Reason: kernel.ReasonEnum,
					Message: "The workflowTool field is one of TERRAFORM_FOSS, CUSTOM, OPEN_TOFU.",
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := iac.ValidateSettings(c.in)
			inv, ok := errors.AsType[*kernel.Invalid](err)
			if !ok {
				t.Fatalf("ValidateSettings = %v, want *kernel.Invalid", err)
			}
			if inv.Detail == "" || !reflect.DeepEqual(inv.Errors, c.want) {
				t.Errorf("ValidateSettings = %q %+v, want %+v", inv.Detail, inv.Errors, c.want)
			}
		})
	}
}

func TestUnitRegistry(t *testing.T) {
	vendors := iac.Vendors()
	if len(vendors) != 2 || vendors[0].Kind() != iac.Terraform ||
		vendors[1].Kind() != iac.OpenTofu {
		t.Fatalf("Vendors = %v", vendors)
	}
	vendors[0] = nil
	if v, ok := iac.Lookup(iac.Terraform); !ok || v == nil {
		t.Error("changing what Vendors returned changed the registry")
	}
	if v, ok := iac.Lookup("pulumi"); ok || v != nil {
		t.Errorf("Lookup(pulumi) = %v, %v; want none", v, ok)
	}
}
