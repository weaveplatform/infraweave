package iac

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/iac/version"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Kind names a vendor as a stack input's vendorConfig names its member.
type Kind string

// Vendor is one infrastructure-as-code tool's knowledge.
type Vendor interface {
	Kind() Kind
	// ValidateSettings checks settings, the vendor's member of a stack
	// input's vendorConfig, and refuses each field the tool would refuse.
	// Each pointer is relative to settings. It reads nothing but settings,
	// so any number of goroutines may call it at once.
	ValidateSettings(settings json.RawMessage) []kernel.FieldError
	// ParsePlan reads the tool's JSON of a saved plan into the plan's
	// changes and delta, as plan.Parse does; any number of goroutines may
	// call it at once.
	ParsePlan(r io.Reader) (plan.Plan, error)
}

// Input is what a stack input carries for its vendor.
type Input struct {
	// VendorConfig is the input's vendorConfig, a JSON object with at most
	// one member that is not null; nil or null when the input has none.
	VendorConfig json.RawMessage
	// TerraformVersion is the input's top-level terraformVersion, a
	// Terraform version constraint that takes precedence over
	// vendorConfig.terraform.version.
	TerraformVersion *string
}

// invalidDetail is the detail of every refused Input.
const invalidDetail = "The stack's vendor settings are not ones its vendor accepts."

// ValidateSettings checks in when a stack is written, so that a setting
// its tool would refuse fails the write rather than the stack's first run.
// It refuses a vendorConfig that sets more than one member or a member no
// registered vendor holds, and each field the member's vendor refuses, as
// a *kernel.Invalid whose pointers lead into the stack input.
func ValidateSettings(in Input) error {
	var errs []kernel.FieldError
	if in.TerraformVersion != nil {
		if _, err := version.ParseConstraints(*in.TerraformVersion); err != nil {
			errs = append(errs, kernel.FieldError{
				Pointer: "/terraformVersion", Reason: kernel.ReasonFormat,
				Message: fmt.Sprintf("The terraformVersion field is %v.", err),
			})
		}
	}
	errs = append(errs, vendorConfig(in.VendorConfig)...)
	if len(errs) == 0 {
		return nil
	}
	return &kernel.Invalid{Detail: invalidDetail, Errors: errs}
}

// pointerEscape escapes a member name as a JSON Pointer reference token.
var pointerEscape = strings.NewReplacer("~", "~0", "/", "~1")

func vendorConfig(raw json.RawMessage) []kernel.FieldError {
	if len(raw) == 0 {
		return nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return []kernel.FieldError{{
			Pointer: "/vendorConfig", Reason: kernel.ReasonType,
			Message: "The vendorConfig field is an object or null.",
		}}
	}
	var set []string
	for name, value := range members {
		if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			set = append(set, name)
		}
	}
	slices.Sort(set)
	var errs []kernel.FieldError
	if len(set) > 1 {
		errs = append(errs, kernel.FieldError{
			Pointer: "/vendorConfig", Reason: kernel.ReasonRule,
			Message: fmt.Sprintf("A stack has one vendor, and vendorConfig sets %s.",
				strings.Join(set, " and ")),
		})
	}
	for _, name := range set {
		at := "/vendorConfig/" + pointerEscape.Replace(name)
		v, ok := Lookup(Kind(name))
		if !ok {
			errs = append(errs, kernel.FieldError{
				Pointer: at, Reason: kernel.ReasonRule,
				Message: fmt.Sprintf("infraweave runs no %s stacks.", name),
			})
			continue
		}
		for _, e := range v.ValidateSettings(members[name]) {
			e.Pointer = at + e.Pointer
			errs = append(errs, e)
		}
	}
	return errs
}
