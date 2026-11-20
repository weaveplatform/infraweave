package acceptance

import (
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitC291HasExecutableRESTReplay(t *testing.T) {
	captures, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, capture := range captures {
		if capture.ID != "C291" {
			continue
		}
		if capture.ReplayAfter != "" || capture.ReplayWith == "" || statefulCaptureReplayers[capture.ReplayWith] == nil {
			t.Fatalf("C291 has no executable REST rejection replay; still deferred to %s", capture.ReplayAfter)
		}
		return
	}
	t.Fatal("C291 capture is missing")
}
