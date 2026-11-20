package report_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Old sessions and already signed/PUT objects cannot bypass the recording
// receipt. Modes must match the run's frozen execution choice, including NONE.
func TestUnitPlanSanitizationReceipts(t *testing.T) {
	for _, expected := range []workersapi.SanitizationMode{workersapi.SanitizationDefault, workersapi.SanitizationSmart, workersapi.SanitizationNone} {
		for _, mode := range []workersapi.SanitizationMode{"", "UNKNOWN", workersapi.SanitizationDefault, workersapi.SanitizationSmart, workersapi.SanitizationNone} {
			t.Run(string(expected)+"/"+string(mode), func(t *testing.T) {
				w := newWorld(kernel.RunTypeTracked, kernel.RunStatePlanning)
				w.s.run.ResolvedConfig = &runtimeconfig.Record{Execution: runtimeconfig.Execution{PlanSanitization: expected}}
				prefix := w.s.run.Account.String() + "/" + w.s.run.ID.String() + "/PLANNING-2/"
				w.o.uploaded["run-artifacts/"+prefix+"plan.json"] = objectstore.Object{Size: 9}
				_, err := w.uploads(t, 2, workersapi.Upload{Kind: workersapi.UploadArtifact, Name: "plan.json", Size: 9, SHA256: sum(1), PlanSanitization: mode})
				var invalid *kernel.Invalid
				if mode != expected {
					if !errors.As(err, &invalid) || len(w.o.begun) != 0 {
						t.Fatalf("legacy/mismatched declaration accepted: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				err = in(t, func(ctx context.Context) error {
					return w.svc.Artifacts(ctx, w.s.run.ID, 2, []workersapi.Recorded{{Kind: workersapi.UploadArtifact, Name: "plan.json", PlanSanitization: mode}})
				})
				if mode != expected {
					if !errors.As(err, &invalid) || len(w.s.recorded) != 0 {
						t.Fatalf("legacy/mismatched recording accepted: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	w := newWorld(kernel.RunTypeTracked, kernel.RunStatePlanning)
	_, err := w.uploads(t, 2, workersapi.Upload{Kind: workersapi.UploadLogChunk, Name: "0", Size: 1, SHA256: sum(1), PlanSanitization: workersapi.SanitizationDefault})
	var invalid *kernel.Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("unrelated receipt accepted: %v", err)
	}
}
