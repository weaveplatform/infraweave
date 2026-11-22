package acceptance

import (
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitPolicyToolingCapturesHaveExecutableRESTProof(t *testing.T) {
	captures, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	full, subsets := captureReplayRegistry(t)
	for _, id := range []string{"C47", "C50", "C52", "C53", "C54", "C273"} {
		t.Run(id, func(t *testing.T) {
			for _, c := range captures {
				if c.ID != id {
					continue
				}
				if c.ReplayAfter != "" {
					t.Fatalf("policy tooling capture still skips real REST proof behind %s", c.ReplayAfter)
				}
				if c.ReplayWith != "" && full[c.ReplayWith] != nil {
					return
				}
				if c.ReplaySubset != "" && subsets[c.ReplaySubset] != nil {
					for pointer, handoff := range c.Handoffs {
						if handoff.Classification == "deferred" {
							t.Errorf("delivered capture retains deferred assertion %s: %s", pointer, handoff.Detail)
						}
					}
					return
				}
				t.Fatal("capture has no executable public-service replayer")
				return
			}
			t.Fatal("required capture is absent")
		})
	}
}
