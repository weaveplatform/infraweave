package policytest

import (
	"encoding/json"
	"io"

	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// PlanVendor projects an opened worker plan through the production typed
// builder, returning its vendor blocks without adding run metadata or gates.
func PlanVendor(vendor string, r io.Reader) (json.RawMessage, error) {
	var in inputs.PlanInput
	if err := inputs.ApplyPlan(&in, vendor, r); err != nil {
		return nil, err //nolint:wrapcheck // the production builder names the document
	}
	raw, err := json.Marshal(in)
	return raw, err //nolint:wrapcheck // typed fields name encoding failures
}
