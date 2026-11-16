package http_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
)

// TestUnitRunCommentBounds holds RunCommentRequest.body's bounds to the
// 1 to control.MaxComment characters a comment holds, which the comment's
// own refusal and run_comments' CHECK enforce too.
func TestUnitRunCommentBounds(t *testing.T) {
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := spec.Components.Schemas["RunCommentRequest"]
	if !ok {
		t.Fatal("the spec has no RunCommentRequest")
	}
	body := ref.Value.Properties["body"].Value
	if body.MinLength != 1 || body.MaxLength == nil || *body.MaxLength != control.MaxComment {
		t.Errorf("RunCommentRequest.body is %d to %v characters, want 1 to %d", body.MinLength,
			body.MaxLength, control.MaxComment)
	}
}

// TestUnitRunTargetedReplanBounds holds RunTargetedReplanRequest.targets'
// bounds to the 1 to kernel.MaxTargets addresses of 1 to kernel.MaxTarget
// characters that kernel.CheckTargets and runs' CHECK enforce too.
func TestUnitRunTargetedReplanBounds(t *testing.T) {
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := spec.Components.Schemas["RunTargetedReplanRequest"]
	if !ok {
		t.Fatal("the spec has no RunTargetedReplanRequest")
	}
	targets := ref.Value.Properties["targets"].Value
	if targets.MinItems != 1 || targets.MaxItems == nil || *targets.MaxItems != kernel.MaxTargets {
		t.Errorf("targets holds %d to %v addresses, want 1 to %d", targets.MinItems,
			targets.MaxItems, kernel.MaxTargets)
	}
	item := targets.Items.Value
	if item.MinLength != 1 || item.MaxLength == nil || *item.MaxLength != kernel.MaxTarget {
		t.Errorf("an address is %d to %v characters, want 1 to %d", item.MinLength,
			item.MaxLength, kernel.MaxTarget)
	}
}
