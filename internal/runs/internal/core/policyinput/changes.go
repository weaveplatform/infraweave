package policyinput

import (
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

// StructuralChanges converts only the facts changes.json actually retains.
// Address parsing preserves quoted module/resource keys; no raw attributes,
// values or provider properties are inferred from an address.
func StructuralChanges(vendor string, changes []plan.Change) ([]policyapi.ChangeFacts, error) {
	if vendor != "terraform" && vendor != "opentofu" && vendor != "terragrunt" {
		return nil, policyapi.ErrInput
	}
	out := make([]policyapi.ChangeFacts, 0, len(changes))
	for _, change := range changes {
		action, ok := policyActions[change.Metadata.Type]
		if !ok || change.Metadata.Result != plan.ResultPlanned || change.Address == "" {
			return nil, policyapi.ErrInput
		}
		fact := policyapi.ChangeFacts{Phase: "plan", Action: action, Address: change.Address, Vendor: vendor, Moved: known(change.Metadata.Moved)}
		if entity, ok := plan.EntityOf(change.Address); ok {
			fact.ResourceType, fact.Name, fact.Module = known(entity.Type), known(entity.Name), known(entity.Module)
		}
		out = append(out, fact)
	}
	return out, nil
}

var policyActions = map[plan.Type]string{
	plan.TypeAdd: "added", plan.TypeChange: "changed", plan.TypeDelete: "deleted",
	plan.TypeNoop: "no-op", plan.TypeRead: "read", plan.TypeImport: "import", plan.TypeForget: "forget",
	plan.TypeCreateAfterForgotten:       "create-after-forgotten",
	plan.TypeReplaceDestroyBeforeCreate: "destroy-Before-create-replaced",
	plan.TypeReplaceCreateBeforeDestroy: "create-Before-destroy-replaced",
}
