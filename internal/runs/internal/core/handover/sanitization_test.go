package handover_test

import (
	"encoding/json"
	"testing"

	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// The public handover freezes each mode and the opt-out before SMART. A resumed
// run keeps that choice when the stack's settings and labels subsequently change.
func TestUnitHandoverSanitization(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		labels         []string
		want           workersapi.SanitizationMode
	}{
		{"default", `{}`, nil, workersapi.SanitizationDefault},
		{"smart", `{"useSmartSanitization":true}`, nil, workersapi.SanitizationSmart},
		{"explicit opt-out", `{"useSmartSanitization":true}`, []string{"feature:disable_resource_sanitization"}, workersapi.SanitizationNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.stacks.settings.VendorConfig = json.RawMessage(tc.settings)
			w.stacks.labels = tc.labels
			h, err := w.handover(t, w.run.ID)
			if err != nil || h.Spec.PlanSanitization != tc.want {
				t.Fatalf("first mode %s: %v", h.Spec.PlanSanitization, err)
			}
			w.stacks.labels = nil
			w.stacks.settings.VendorConfig = json.RawMessage(`{}`)
			h, err = w.handover(t, w.run.ID)
			if err != nil || h.Spec.PlanSanitization != tc.want {
				t.Fatalf("resumed mode %s: %v", h.Spec.PlanSanitization, err)
			}
			w.store.run.ResolvedConfig.Execution.PlanSanitization = ""
			h, err = w.handover(t, w.run.ID)

			if err != nil || h.Spec.PlanSanitization != workersapi.SanitizationDefault {
				t.Fatalf("older record default %s: %v", h.Spec.PlanSanitization, err)
			}
		})
	}
}
