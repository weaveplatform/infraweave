package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

func TestUnitSimulationUsesTheLiveRuntimeAndFullDefaultsWithoutPersistencePorts(t *testing.T) {
	runtime, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	// Any attempt to select policies, store evidence or apply a decision would
	// reach a missing port; the simulator needs only the actual compiler.
	svc := New(Deps{Runtime: runtime})
	for _, engine := range []rego.EngineType{"", rego.V0, rego.V1} {
		body := "package infraweave\nwarn contains \"edited\" if input.value\n"
		if engine == rego.V0 {
			body = "package infraweave\nwarn[\"edited\"] { input.value }\n"
		}
		result, err := svc.Simulate(t.Context(), rego.Request{Policy: rego.Policy{Type: rego.Plan, Engine: engine, Body: body}, Input: json.RawMessage(`{"value":true}`)})
		var got map[string]any
		if err != nil || json.Unmarshal(result, &got) != nil {
			t.Fatalf("dialect %s: %s %v", engine, result, err)
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(`{"deny":[],"flag":[],"info":[],"reset_flag":[],"sample":false,"warn":["edited"]}`), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("simulation lost live defaults: %s", result)
		}
	}
}

func TestUnitSimulationRefusesInvalidSourceInputAndRaisedOperatorLimits(t *testing.T) {
	settings := rego.Defaults()
	settings.MaxBodyBytes, settings.MaxSimulationBytes, settings.MaxInputBytes = 512<<10, 8<<20, 8<<20
	runtime, err := rego.New(settings)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Deps{Runtime: runtime})
	for name, req := range map[string]rego.Request{
		"wrong namespace": {Policy: rego.Policy{Type: rego.Plan, Body: "package elsewhere"}, Input: json.RawMessage(`{}`)},
		"invalid Rego":    {Policy: rego.Policy{Type: rego.Plan, Body: "package infraweave\nwarn contains"}, Input: json.RawMessage(`{}`)},
		"disabled exec":   {Policy: rego.Policy{Type: rego.Plan, Body: "package infraweave\nwarn contains exec(\"id\")"}, Input: json.RawMessage(`{}`)},
		"invalid JSON":    {Policy: rego.Policy{Type: rego.Plan, Body: "package infraweave"}, Input: json.RawMessage(`{"private-input":`)},
		"trailing JSON":   {Policy: rego.Policy{Type: rego.Plan, Body: "package infraweave"}, Input: json.RawMessage(`{} {}`)},
		"body ceiling":    {Policy: rego.Policy{Type: rego.Plan, Body: strings.Repeat(" ", simulationBodyBytes+1)}, Input: json.RawMessage(`{}`)},
		"input ceiling":   {Policy: rego.Policy{Type: rego.Plan, Body: "package infraweave"}, Input: json.RawMessage(strings.Repeat(" ", simulationInputBytes+1))},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Simulate(t.Context(), req)
			var invalid *kernel.Invalid
			if !errors.As(err, &invalid) || len(invalid.Errors) == 0 || strings.Contains(err.Error(), "private-input") {
				t.Fatalf("invalid simulation succeeded or leaked edited input: %v", err)
			}
		})
	}
	if _, err := New(Deps{}).Simulate(t.Context(), rego.Request{}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("missing runtime accepted simulation: %v", err)
	}
}

func TestUnitSimulationKeepsConfiguredLimitsCancellationAndPerTypeDeadline(t *testing.T) {
	settings := rego.Defaults()
	settings.MaxSimulationBytes = 2
	settings.Timeouts.Plan = 10 * time.Millisecond
	runtime, err := rego.New(settings)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Deps{Runtime: runtime})
	req := rego.Request{Policy: rego.Policy{Type: rego.Plan, Body: "package infraweave"}, Input: json.RawMessage(`[1]`)}
	if _, err := svc.Simulate(t.Context(), req); err == nil {
		t.Fatal("configured simulation input limit ignored")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.Simulate(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation disappeared: %v", err)
	}
	req.Input = json.RawMessage(`{}`)
	req.Policy.Engine = rego.V1
	req.Policy.Body = `package infraweave
values := numbers.range(0,299)
warn contains "never" if {
  some a in values
  some b in values
  some c in values
  some d in values
  a+b+c+d == -1
}`
	warm, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err := warm.Check(t.Context(), req.Policy); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Simulate(t.Context(), req)
	var invalid *kernel.Invalid
	if !errors.As(err, &invalid) || len(invalid.Errors) != 1 || invalid.Errors[0].Message != "Policy evaluation timed out." {
		t.Fatalf("type deadline became an allowed decision: %v", err)
	}
}
