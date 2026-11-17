package trigger

import (
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

// CheckOverrides checks proposal overrides under /environmentVarsOverrides,
// using the runtime configuration's shared variable rules.
//
// ref: decision 0008 § "The run specification and its secrets"
func CheckOverrides(ovs []lifecycle.Override) error {
	vars := make([]runtimeconfig.Variable, len(ovs))
	for i, o := range ovs {
		vars[i] = runtimeconfig.Variable{Key: o.Name, Value: o.Value}
	}
	return runtimeconfig.CheckVariables(vars, "/environmentVarsOverrides") //nolint:wrapcheck // the shared validator names each refusal
}
