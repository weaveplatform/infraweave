package http_test

import (
	"testing"

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
