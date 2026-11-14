package surface_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/workflow"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/surface"
)

// TestUnitWorkflowSurface describes each vendor with sample run
// parameters, refuses a kind no vendor has as no context, and renders a
// template against a sample as the runner would, every value one shell
// word.
func TestUnitWorkflowSurface(t *testing.T) {
	s := surface.Workflow()
	if s.Name != workflow.Surface || !s.Shell || len(s.Variables) != len(workflow.Variables) {
		t.Fatalf("surface %+v", s)
	}
	reg, err := expr.NewRegistry(expr.Defaults(), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"terraform", "opentofu"} {
		c, err := s.Env(t.Context(), kernel.Principal{}, kind)
		if err != nil || c.Sample["planFileName"] != workflow.PlanFile ||
			c.Sample["lockTimeout"] != "5m0s" {
			t.Fatalf("%s: %+v, %v", kind, c, err)
		}
		c.Sample["workspaceName"] = "x; touch pwned"
		res, err := reg.Evaluate(t.Context(), expr.Request{
			Surface: workflow.Surface, Form: expr.FormTemplate,
			Source: "tofu workspace select ${{ workspaceName }} -lock=${{ lock }}",
		}, expr.Input{Values: c.Sample})
		if err != nil || res.Value != "tofu workspace select 'x; touch pwned' -lock='true'" {
			t.Errorf("%s: %v, %v", kind, res.Value, err)
		}
	}
	if _, err := s.Env(
		t.Context(),
		kernel.Principal{},
		"pulumi",
	); !errors.Is(
		err,
		expr.ErrNoContext,
	) {
		t.Errorf("an unknown vendor: %v", err)
	}
}
