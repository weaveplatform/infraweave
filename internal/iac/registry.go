package iac

import (
	"encoding/json"
	"slices"

	"github.com/weaveplatform/infraweave/internal/iac/opentofu"
	"github.com/weaveplatform/infraweave/internal/iac/terraform"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The registered vendors' kinds.
const (
	Terraform Kind = terraform.Kind
	OpenTofu  Kind = opentofu.Kind
)

// vendor adapts a vendor package's functions to Vendor. The vendor
// packages cannot implement Vendor themselves, since this package imports
// them to register them and they would have to import it for Kind.
type vendor struct {
	kind     Kind
	validate func(json.RawMessage) []kernel.FieldError
}

func (v vendor) Kind() Kind { return v.kind }

func (v vendor) ValidateSettings(settings json.RawMessage) []kernel.FieldError {
	return v.validate(settings)
}

// registry is every vendor infraweave runs, in the order Vendors lists
// them.
var registry = []Vendor{
	vendor{kind: Terraform, validate: terraform.ValidateSettings},
	vendor{kind: OpenTofu, validate: opentofu.ValidateSettings},
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
