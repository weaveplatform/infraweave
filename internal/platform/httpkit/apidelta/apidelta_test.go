package apidelta_test

import (
	"math"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apidelta"
)

// TestUnitOf answers no delta for none, and every count of a delta,
// clamped to int32, with the Ansible counts zero.
func TestUnitOf(t *testing.T) {
	if d := apidelta.Of(nil); d != nil {
		t.Errorf("no delta = %+v", d)
	}
	got := apidelta.Of(&kernel.PlanDelta{
		Add: 1, Change: 2, Delete: 3, Import: 4, Move: 5, Forget: 6, Resources: math.MaxInt32 + 1,
		Outputs: true,
	})
	if *got != (api.Delta{
		AddCount: 1, ChangeCount: 2, DeleteCount: 3, ImportCount: 4, MoveCount: 5,
		ForgetCount: 6, Resources: math.MaxInt32,
	}) {
		t.Errorf("delta = %+v", got)
	}
}
