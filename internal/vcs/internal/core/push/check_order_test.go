package push

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

type checkOrderedIntegration struct {
	Integrations
	world *evaluationWorld
}

func (i checkOrderedIntegration) HeldEventUse(ctx context.Context, account kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	i.world.step(ctx, "integration hold")
	return i.Integrations.HeldEventUse(ctx, account, id)
}

type checkOrderedCap struct{ world *evaluationWorld }

func (c checkOrderedCap) Capped(ctx context.Context, _ kernel.AccountID, _, _ kernel.ULID, _ string, _ kernel.RunType, _ int) error {
	c.world.step(ctx, "cap identity")
	return nil
}

func TestUnitCappedEvaluationHoldsIntegrationBeforeStack(t *testing.T) {
	w := newEvaluation(t)
	w.cap = 0
	w.events = []Prepared{w.push("main", strings.Repeat("a", 40), "main.tf")}
	w.s.d.Integrations = checkOrderedIntegration{Integrations: w.s.d.Integrations, world: w}
	w.s.d.Checks = checkOrderedCap{w}
	if err := w.s.Evaluated(t.Context(), w.job); err != nil {
		t.Fatal(err)
	}
	integration, stack, cap := slices.Index(w.order, "integration hold"), slices.Index(w.order, "stack lock"), slices.Index(w.order, "cap identity")
	if integration < 0 || stack < integration || cap < stack {
		t.Fatal("capped evaluator inverted lifecycle's parent-before-stack order", w.order)
	}
}
