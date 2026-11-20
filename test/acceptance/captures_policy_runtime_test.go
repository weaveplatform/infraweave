package acceptance

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitC57RuntimeReplayHandoff(t *testing.T) {
	captures, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, capture := range captures {
		if capture.ID != "C57" {
			continue
		}
		if capture.ReplayAfter != "" || capture.ReplayWith != "policy-runtime" || len(capture.Calls) != 0 {
			t.Fatalf("C57 requires its dynamic runtime REST replay; deferred to %q, replayer %q", capture.ReplayAfter, capture.ReplayWith)
		}
		return
	}
	t.Fatal("missing C57 capture")
}

func replayPolicyRuntime(t *testing.T, client *sdk.Client) {
	t.Helper()
	response, err := client.Do(t.Context(), "GET", "/api/v1/policy-runtime", nil)
	if err != nil || response == nil || response.Status != 200 {
		t.Fatalf("C57 runtime REST read: %+v %v", response, err)
	}
	version, err := harness.PolicyRuntimeVersion(response.Body)
	if err != nil {
		t.Fatalf("C57 runtime metadata: %v", err)
	}
	t.Logf("C57 authenticated REST replay verified linked OPA %s; dated upstream observation retained", version)
}

func validateCaptureReplayers(captures []harness.Capture, registry map[string]func(*testing.T, *sdk.Client)) error {
	for _, capture := range captures {
		if capture.ReplayWith != "" && registry[capture.ReplayWith] == nil {
			return fmt.Errorf("%w: capture %s names unknown REST replayer %q", harness.ErrCapture, capture.ID, capture.ReplayWith)
		}
	}
	return nil
}

func TestUnitCaptureReplayRegistryRefusals(t *testing.T) {
	capture := harness.Capture{ID: "C57", ReplayWith: "policy-runtime"}
	for name, registry := range map[string]map[string]func(*testing.T, *sdk.Client){
		"missing": {}, "nil handler": {"policy-runtime": nil},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCaptureReplayers(
				[]harness.Capture{capture},
				registry,
			); err == nil || !strings.Contains(err.Error(), "C57") ||
				!strings.Contains(err.Error(), "policy-runtime") {
				t.Fatalf("invalid capture registry accepted: %v", err)
			}
		})
	}
	if err := validateCaptureReplayers([]harness.Capture{capture}, statefulCaptureReplayers); err != nil {
		t.Fatal(err)
	}
}
