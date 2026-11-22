package inputs_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func TestUnitCapturePreservesTypedFixtureShapesAndOriginalSamples(t *testing.T) {
	files, err := filepath.Glob("../../../../test/fixtures/policy-inputs/*.json")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		if strings.Contains(file, ".decision.") {
			continue
		}
		name := filepath.Base(file)
		kind := strings.ToUpper(strings.Split(name, "-")[0])
		if kind == "PUSH" {
			kind = "GIT_PUSH"
		}
		if kind == "ACCESS" {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.ReplaceAll(raw, []byte(`"spacelift"`), []byte(`"infraweave"`))
		saved := bytes.Clone(raw)
		got, err := inputs.Capture(kind, raw)
		if err != nil {
			t.Fatalf("%s capture: %v", name, err)
		}
		if err := inputs.Validate(kind, got); err != nil {
			t.Fatalf("%s sanitized contract: %v", name, err)
		}
		var before, after any
		if err := json.Unmarshal(raw, &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(got, &after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(captureShape(before), captureShape(after)) || !bytes.Equal(raw, saved) {
			t.Fatalf("capture changed shape or original sample: %s", name)
		}
		checked++
	}
	if checked < 10 {
		t.Fatalf("only %d input fixtures checked", checked)
	}
}

func captureShape(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, child := range value {
			out[k] = captureShape(child)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = captureShape(child)
		}
		return out
	default:
		return reflect.TypeOf(value)
	}
}

func TestUnitCaptureStripsDeclaredPrivateValuesAndPreservesMetadata(t *testing.T) {
	raw, err := os.ReadFile("../../../../test/fixtures/policy-inputs/plan-tracked-warn.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte(`"spacelift"`), []byte(`"infraweave"`))
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	envelope := value["infraweave"].(map[string]any)
	run := envelope["run"].(map[string]any)
	run["command"] = "private-command"
	run["user_provided_metadata"] = []any{"private-metadata"}
	run["runtime_config"] = map[string]any{
		"environment":       map[string]any{"TOKEN": "private-env"},
		"project_root":      "infra",
		"runner_image":      "runner:1",
		"terraform_version": nil,
		"before_init":       []any{"private-hook"},
	}
	value["third_party_metadata"] = map[string]any{"custom": map[string]any{"plugin": map[string]any{"value": "private-plugin", "count": 3, "enabled": true, "null": nil}}}
	raw, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	got, err := inputs.Capture("PLAN", raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("private-")) || !bytes.Contains(got, []byte(`"project_root":"infra"`)) || !bytes.Contains(got, []byte(`"runner_image":"runner:1"`)) {
		t.Fatal("capture leaked declared private values or removed structural metadata")
	}
	contract, err := inputs.Contract()
	if err != nil {
		t.Fatal(err)
	}
	if !contract.Defs["RuntimeConfig"].Properties["environment"].CaptureSensitive || !contract.Defs["PlanRun"].Properties["command"].CaptureSensitive {
		t.Fatal("published contract does not declare capture privacy fields")
	}
}
