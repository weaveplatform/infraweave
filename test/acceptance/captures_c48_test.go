package acceptance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func c48Handoff(t *testing.T) harness.Capture {
	t.Helper()
	all, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, capture := range all {
		if capture.ID == "C48" {
			return capture
		}
	}
	t.Fatal("missing C48 capture")
	return harness.Capture{}
}

func TestUnitC48ReplayNoLongerWaitsForClosedWorkflowTickets(t *testing.T) {
	capture := c48Handoff(t)
	if capture.ReplayAfter != "" || capture.ReplayWith != "" || capture.ReplaySubset != "policy-free-tier-management" {
		t.Fatalf("C48 must execute its REST subset with explicit upstream handoffs: %+v", capture)
	}
	_, subsets := captureReplayRegistry(t)
	if err := validateCaptureSubsetReplayers([]harness.Capture{capture}, subsets); err != nil {
		t.Fatal(err)
	}
	for _, ptr := range []string{c48Notification, c48Stacks, c48Policies} {
		if capture.Handoffs[ptr].Classification != "rest" {
			t.Fatalf("C48 %s lost its executable REST assertion", ptr)
		}
	}
	state := capture.Handoffs[c48Workflow]
	if state.Classification != "rest" || state.Owner != "https://github.com/weaveplatform/infraweave/issues/656" {
		t.Fatalf("C48 FAILED run lacks its executed workflow handoff: %+v", state)
	}
}

func TestUnitCaptureSubsetRegistryAndExecutionRefusals(t *testing.T) {
	capture := c48Handoff(t)
	for name, registry := range map[string]map[string]func(*testing.T, *sdk.Client) []string{
		"absent": {}, "nil handler": {capture.ReplaySubset: nil},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCaptureSubsetReplayers([]harness.Capture{capture}, registry); !errors.Is(err, harness.ErrCapture) || !strings.Contains(err.Error(), "C48") {
				t.Fatalf("invalid subset registry accepted: %v", err)
			}
		})
	}
	for name, executed := range map[string][]string{
		"empty":     nil,
		"partial":   {c48Stacks},
		"unknown":   {c48Stacks, c48Policies, c48Notification, c48Workflow, "/unknown"},
		"upstream":  {c48Stacks, c48Policies, c48Notification, c48Workflow, "/exchanges/0/response/data/tier"},
		"duplicate": {c48Stacks, c48Policies, c48Notification, c48Workflow, c48Notification},
	} {
		t.Run(name, func(t *testing.T) {
			if err := capture.CheckSubset(executed); !errors.Is(err, harness.ErrParity) {
				t.Fatalf("invalid execution counted: %v", err)
			}
		})
	}
	if err := capture.CheckSubset([]string{c48Policies, c48Stacks, c48Notification, c48Workflow}); err != nil {
		t.Fatal(err)
	}
	capture.ReplaySubset = ""
	if err := capture.CheckSubset([]string{c48Policies, c48Stacks, c48Notification, c48Workflow}); !errors.Is(err, harness.ErrParity) {
		t.Fatalf("full replay counted as subset: %v", err)
	}
}

func TestUnitCaptureHandoffMetadataRefusals(t *testing.T) {
	source := filepath.Join(capturesRoot, "C48")
	original, err := os.ReadFile(filepath.Join(source, harness.ExpectFile))
	if err != nil {
		t.Fatal(err)
	}
	handoffs := func(doc map[string]any) map[string]any { return doc["handoffs"].(map[string]any) }
	one := func(doc map[string]any) map[string]any { return handoffs(doc)[c48Stacks].(map[string]any) }
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"missing mapping", func(d map[string]any) { delete(handoffs(d), c48Stacks) }, "no handoff"},
		{"extra mapping", func(d map[string]any) { handoffs(d)["/extra"] = one(d) }, "exactly"},
		{"unknown classification", func(d map[string]any) { one(d)["classification"] = "passed" }, "invalid handoff"},
		{"empty owner", func(d map[string]any) { one(d)["owner"] = "  " }, "invalid handoff"},
		{"empty detail", func(d map[string]any) { one(d)["detail"] = "\t" }, "invalid handoff"},
		{"no REST", func(d map[string]any) {
			for _, v := range handoffs(d) {
				v.(map[string]any)["classification"] = "deferred"
			}
		}, "both REST"},
		{"no remaining handoffs", func(d map[string]any) {
			for _, v := range handoffs(d) {
				v.(map[string]any)["classification"] = "rest"
			}
		}, "both REST"},
		{"mixed static calls", func(d map[string]any) {
			d["calls"] = []any{map[string]any{"method": "GET", "path": "/", "expect": map[string]any{"status": 200}}}
		}, "cannot mix"},
		{"mixed full replay", func(d map[string]any) { d["replayWith"] = "policy-runtime" }, "cannot mix"},
		{"mixed deferral", func(d map[string]any) { d["replayAfter"] = "issue" }, "cannot mix"},
		{"orphaned handoffs", func(d map[string]any) { delete(d, "replaySubset"); d["replayAfter"] = "issue" }, "require a subset"},
		{"unknown handoff key", func(d map[string]any) { one(d)["secretSuccess"] = true }, "field secretSuccess not found"},
		{"unrecorded assertion", func(d map[string]any) {
			h := handoffs(d)
			h["/missing"] = h[c48Stacks]
			delete(h, c48Stacks)
			o := d["observed"].(map[string]any)
			o["/missing"] = o[c48Stacks]
			delete(o, c48Stacks)
		}, "observed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := yaml.Unmarshal(original, &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(doc)
			updated, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			dir := filepath.Join(root, "C48")
			if err := os.Mkdir(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{harness.MetaFile, harness.RequestFile, harness.ResponseFile, harness.ExpectFile} {
				data := updated
				if name != harness.ExpectFile {
					data, err = os.ReadFile(filepath.Join(source, name))
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err = harness.LoadCaptures(root, map[string]bool{"C48": true})
			if !errors.Is(err, harness.ErrCapture) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadCaptures=%v,want %s", err, tc.want)
			}
		})
	}
}
