package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

func TestUnitCheckSharesSaveDiagnostics(t *testing.T) {
	runtime, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Deps{Runtime: runtime})
	for _, engine := range []rego.EngineType{"", rego.V0, rego.V1} {
		for _, source := range []string{
			"package infraweave\nwarn contains \"oops\" if {\n",
			"package infraweave\nwarn contains \"é🦉\" if { missing }\n",
			"package infraweave\nwarn := true\n",
			"package infraweave\nimport rego.v2\n",
			"package infraweave\nwarn contains http.send({\"url\": \"https://invalid.test\"})\n",
			"package elsewhere\n",
		} {
			fields, err := svc.Check(t.Context(), kernel.Principal{}, rego.Policy{Type: rego.Plan, Engine: engine, Body: source})
			if err != nil || len(fields) == 0 {
				t.Fatalf("engine %s: check accepted %q: %+v %v", engine, source, fields, err)
			}
			_, err = svc.Create(t.Context(), kernel.Principal{}, Input{Name: "Invalid", Type: rego.Plan, Engine: engine, Body: source})
			var invalid *kernel.Invalid
			if !errors.As(err, &invalid) || !reflect.DeepEqual(fields, invalid.Errors) {
				t.Fatalf("check/save disagree: %+v; %v", fields, err)
			}
		}
	}
	for _, engine := range []rego.EngineType{"", rego.V0, rego.V1} {
		for _, kind := range []rego.Type{rego.Plan, rego.Approval, rego.Push, rego.Trigger, rego.Login, rego.Access, rego.Initialization, rego.Task, rego.Notification, rego.Intent} {
			fields, err := svc.Check(t.Context(), kernel.Principal{}, rego.Policy{Type: kind, Engine: engine, Body: "package infraweave\n"})
			if err != nil || fields == nil || len(fields) != 0 {
				t.Fatalf("%s/%s: %+v %v", kind, engine, fields, err)
			}
		}
	}
}

func TestUnitCheckPreservesLimitsAndFailures(t *testing.T) {
	settings := rego.Defaults()
	settings.MaxBodyBytes = 30
	runtime, err := rego.New(settings)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Deps{Runtime: runtime})
	fields, err := svc.Check(t.Context(), kernel.Principal{}, rego.Policy{Type: rego.Plan, Body: strings.Repeat("x", 31)})
	if err != nil || len(fields) != 1 || !strings.Contains(fields[0].Message, "30 bytes") || fields[0].Range != nil {
		t.Fatalf("body limit lost: %+v %v", fields, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.Check(ctx, kernel.Principal{}, rego.Policy{Type: rego.Plan, Body: "package infraweave"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
	if _, err := New(Deps{}).Check(t.Context(), kernel.Principal{}, rego.Policy{}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("missing compiler accepted: %v", err)
	}
	if svc.CheckSurface().Name != "policy" || svc.CheckSurface().CheckPolicy == nil {
		t.Fatal("policy owner omitted its compiler surface")
	}
	fields, err = svc.CheckSurface().CheckPolicy(t.Context(), kernel.Principal{}, expr.PolicySource{Type: "PLAN", Engine: "REGO_V0", Body: "package infraweave"})
	if err != nil || fields == nil || len(fields) != 0 {
		t.Fatalf("compiler contribution lost source metadata: %+v %v", fields, err)
	}
}

func TestUnitCompilerFailureCannotBecomeSuccessfulCheck(t *testing.T) {
	err := New(Deps{}).compileError(&rego.CompileError{}, "package infraweave")
	var invalid *kernel.Invalid
	if err == nil || errors.As(err, &invalid) {
		t.Fatalf("compiler failure became an empty invalid-source result: %v", err)
	}
}
