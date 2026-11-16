package iac

import (
	"encoding/json"
	"io"
	"slices"

	"github.com/weaveplatform/infraweave/internal/iac/opentofu"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/iac/terraform"
	"github.com/weaveplatform/infraweave/internal/iac/terragrunt"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The registered vendors' kinds.
const (
	Terraform  Kind = terraform.Kind
	OpenTofu   Kind = opentofu.Kind
	Terragrunt Kind = terragrunt.Kind
)

// vendor adapts a vendor package's functions to Vendor. The vendor
// packages cannot implement Vendor themselves, since this package imports
// them to register them and they would have to import it for Kind. A
// vendor without state keeps manageState as its stack was created with
// it.
type vendor struct {
	kind     Kind
	validate func(json.RawMessage) []kernel.FieldError
	plan     func(io.Reader) (plan.Plan, error)
	state    func(json.RawMessage, bool) bool
}

func (v vendor) Kind() Kind { return v.kind }

func (v vendor) ValidateSettings(settings json.RawMessage) []kernel.FieldError {
	return v.validate(settings)
}

func (v vendor) ParsePlan(r io.Reader) (plan.Plan, error) { return v.plan(r) }

func (v vendor) ManagesState(settings json.RawMessage, manageState bool) bool {
	if v.state == nil {
		return manageState
	}
	return v.state(settings, manageState)
}

// registry is every vendor infraweave runs, in the order Vendors lists
// them. OpenTofu writes Terraform's plan format, and each Terragrunt
// unit's plan is its tool's, so all three read plans with plan.Parse.
var registry = []Vendor{
	vendor{kind: Terraform, validate: terraform.ValidateSettings, plan: plan.Parse},
	vendor{kind: OpenTofu, validate: opentofu.ValidateSettings, plan: plan.Parse},
	vendor{
		kind: Terragrunt, validate: terragrunt.ValidateSettings, plan: plan.Parse,
		state: terragrunt.ManagesState,
	},
}

// Lookup is the registered vendor of kind k.
func Lookup(k Kind) (Vendor, bool) {
	i := slices.IndexFunc(registry, func(v Vendor) bool { return v.Kind() == k })
	if i < 0 {
		return nil, false
	}
	return registry[i], true
}

// Vendors is every registered vendor.
func Vendors() []Vendor { return slices.Clone(registry) }
