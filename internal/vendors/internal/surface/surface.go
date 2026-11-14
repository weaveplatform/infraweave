package surface

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/workflow"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
)

// sampleLockTimeout is launcher.state_lock_timeout's default, which a
// sample's lockTimeout shows.
const sampleLockTimeout = 5 * time.Minute

// Workflow is the workflow templates' surface: the run parameters, on a
// shell surface, whose contexts are the vendors internal/iac registers,
// named by kind, such as terraform.
//
// ref: decision 0024 "Workflow command templates"
func Workflow() expr.Surface {
	vars := make([]expr.Variable, len(workflow.Variables))
	for i, v := range workflow.Variables {
		vars[i] = expr.Variable{Name: v.Name, Schema: v.Schema}
	}
	return expr.Surface{Name: workflow.Surface, Variables: vars, Shell: true, Env: describe}
}

// describe describes the vendor ref as the surface's context, with sample
// run parameters: a run that plans every resource with the lock, the
// refresh and the default workspace. Every principal reads every vendor,
// since a vendor holds no account's data; a kind no vendor has is
// expr.ErrNoContext.
func describe(_ context.Context, _ kernel.Principal, ref string) (expr.Context, error) {
	if _, ok := iac.Lookup(iac.Kind(ref)); !ok {
		return expr.Context{}, fmt.Errorf("%w: no vendor %q", expr.ErrNoContext, ref)
	}
	return expr.Context{Sample: workflow.Params{
		PlanFileName: workflow.PlanFile, Lock: true, Refresh: true,
		WorkspaceName: workflow.DefaultWorkspace, LockTimeout: sampleLockTimeout,
	}.Values()}, nil
}
