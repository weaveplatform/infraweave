package apidelta

import (
	"math"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// Of is d as the API answers it, nil for no delta.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func Of(d *kernel.PlanDelta) *api.Delta {
	if d == nil {
		return nil
	}
	return &api.Delta{
		AddCount: count(d.Add), ChangeCount: count(d.Change), DeleteCount: count(d.Delete),
		ImportCount: count(d.Import), MoveCount: count(d.Move), ForgetCount: count(d.Forget),
		Resources: count(d.Resources),
	}
}

// count is n as the API's int32 counts carry it.
func count(n int) int32 { return int32(min(n, math.MaxInt32)) } //nolint:gosec // clamped above
