package rego

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	opaprint "github.com/open-policy-agent/opa/v1/topdown/print"
)

func TestUnitPolicySettings(t *testing.T) {
	for _, field := range []string{"Plan", "Push", "Notification", "Login", "Approval", "Trigger", "Intent", "Task", "Initialization", "Access", "Combined", "PushEvent"} {
		s := Defaults()
		reflect.ValueOf(&s.Timeouts).Elem().FieldByName(field).SetInt(0)
		if err := s.Validate(); !errors.Is(err, ErrSettings) {
			t.Errorf("zero %s accepted: %v", field, err)
		}
	}
	for _, field := range []string{"MaxInputBytes", "MaxBodyBytes", "MaxSimulationBytes"} {
		s := Defaults()
		reflect.ValueOf(&s).Elem().FieldByName(field).SetInt(-1)
		if _, err := New(s); !errors.Is(err, ErrSettings) {
			t.Errorf("negative %s accepted: %v", field, err)
		}
	}
	if Defaults().timeout(Type("unknown")) != 0 {
		t.Fatal("unknown type acquired a timeout")
	}
}

func TestUnitPolicyLimits(t *testing.T) {
	s := Defaults()
	s.MaxInputBytes = 8
	s.MaxBodyBytes = 64
	s.MaxSimulationBytes = 2
	e, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	valid := Policy{Type: Plan, Engine: V1, Body: "package infraweave"}
	for _, p := range []Policy{
		{Type: "unknown", Engine: V1, Body: valid.Body},
		{Type: Plan, Engine: "REGO_V2", Body: valid.Body},
		{Type: Plan, Engine: V1, Body: strings.Repeat(" ", 65)},
	} {
		if err := e.Check(t.Context(), p); err == nil {
			t.Fatal("invalid policy accepted")
		}
		if _, err := e.Evaluate(t.Context(), Request{Policy: p, Input: json.RawMessage(`{}`)}); err == nil {
			t.Fatal("invalid policy evaluated")
		}
	}
	for _, input := range []string{"", `broken`, `{} {}`} {
		if _, err := e.Evaluate(t.Context(), Request{Policy: valid, Input: json.RawMessage(input)}); !errors.Is(err, ErrInput) {
			t.Fatalf("invalid JSON %q: %v", input, err)
		}
	}
	if _, err := e.Evaluate(t.Context(), Request{Policy: valid, Input: json.RawMessage(`{"a":123}`)}); !errors.Is(err, ErrLimit) {
		t.Fatalf("input limit: %v", err)
	}
	if _, err := e.Evaluate(t.Context(), Request{Policy: valid, Input: json.RawMessage(`[1]`), Simulation: true}); !errors.Is(err, ErrLimit) {
		t.Fatalf("simulation limit: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.Evaluate(ctx, Request{Policy: valid, Input: json.RawMessage(`{}`)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled input evaluated: %v", err)
	}
	if _, err := e.Evaluate(t.Context(), Request{Policy: Policy{Type: Plan, Engine: V1, Body: "package wrong"}, Input: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("bad package evaluated")
	}
}

func TestUnitPolicyResultBounds(t *testing.T) {
	e := testEngine(t)
	p := Policy{Type: Plan, Engine: V1, Body: `package infraweave
deny contains message if { some message in input.messages }`}
	messages := make([]string, MessageCount+5)
	for i := range messages {
		messages[i] = strings.Repeat("世", MessageBytes) + strings.Repeat("a", i)
	}
	input, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Evaluate(t.Context(), Request{Policy: p, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	value := decision(t, out)
	kept := value["deny"].([]any)
	if len(kept) != MessageCount {
		t.Fatalf("kept %d messages", len(kept))
	}
	for _, message := range kept {
		if text := message.(string); len(text) > MessageBytes || !utf8.ValidString(text) {
			t.Fatal("message bound breaks UTF-8")
		}
	}
	for _, body := range []string{
		`package infraweave
deny contains 1 if true`,
		`package infraweave
deny contains {} if true`,
	} {
		if _, err := e.Evaluate(t.Context(), Request{Policy: Policy{Type: Plan, Engine: V1, Body: body}, Input: json.RawMessage(`{}`)}); !errors.Is(err, ErrResult) {
			t.Fatalf("wrong message type accepted: %v", err)
		}
	}
	for _, body := range []string{
		`package infraweave
roles["root"] := ["space-reader"] if true`,
		`package infraweave
roles["root"] contains "space-reader" if true`,
		`package infraweave
roles["root"]["space-reader"] := true`,
	} {
		evaluate(t, e, Policy{Type: Login, Engine: V1, Body: body}, `{}`)
	}
	// Each output kind is checked after evaluation; wrong shapes fail closed.
	base := decision(t, evaluate(t, e, Policy{Type: Login, Engine: V1, Body: "package infraweave"}, `{}`))
	for _, bad := range []any{true, []any{1}, map[string]any{"root": 1}, map[string]any{"root": []any{false}}, map[string]any{"root": map[string]any{"reader": "yes"}}} {
		base["roles"] = bad
		if err := boundDecision(Login, base); !errors.Is(err, ErrResult) {
			t.Errorf("bad roles accepted: %#v", bad)
		}
	}
	for _, kind := range []Type{Plan, Push, Notification} {
		base := decision(t, evaluate(t, e, Policy{Type: kind, Engine: V1, Body: "package infraweave"}, `{}`))
		if err := boundDecision(kind, map[string]any{}); !errors.Is(err, ErrResult) {
			t.Fatal("empty result accepted")
		}
		for name := range base {
			old := base[name]
			base[name] = 123
			if err := boundDecision(kind, base); !errors.Is(err, ErrResult) {
				t.Fatalf("invalid %s accepted", name)
			}
			base[name] = old
		}
	}
	printed := &printBuffer{}
	if err := printed.Print(opaprint.Context{}, strings.Repeat("a", PrintBytes)); err != nil {
		t.Fatal(err)
	}
	if err := printed.Print(opaprint.Context{}, "ignored"); err != nil || !printed.truncated {
		t.Fatal("print overflow changed evaluation")
	}
}

func TestUnitLinkedVersion(t *testing.T) {
	for _, tc := range []struct {
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{nil, false, ""},
		{nil, true, ""},
		{&debug.BuildInfo{}, true, ""},
		{&debug.BuildInfo{Deps: []*debug.Module{{Path: "other", Version: "v1"}}}, true, ""},
		{&debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/open-policy-agent/opa", Version: "v1.22.4"}}}, true, "v1.22.4"},
		{&debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/open-policy-agent/opa", Replace: &debug.Module{Version: "v1.22.4"}}}}, true, "v1.22.4"},
		{&debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/open-policy-agent/opa", Replace: &debug.Module{Path: "../opa"}}}}, true, ""},
		{&debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/open-policy-agent/opa", Version: "(devel)"}}}, true, ""},
	} {
		got, err := linkedVersion(tc.info, tc.ok)
		if got != tc.want || (tc.want == "" && !errors.Is(err, ErrVersion)) {
			t.Fatalf("version %q %v, want %q", got, err, tc.want)
		}
	}
	_, _ = Version()
}

func TestUnitDiagnostics(t *testing.T) {
	e := testEngine(t)
	err := e.Check(t.Context(), Policy{Type: Plan, Engine: V1, Body: "package wrong"})
	var compiled *CompileError
	if !errors.As(err, &compiled) || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].File != "policy.rego" || compiled.Diagnostics[0].Row != 1 ||
		compiled.Diagnostics[0].Length == 0 ||
		compiled.Error() == "" ||
		compiled.Unwrap() == nil {
		t.Fatalf("lost location: %#v %v", compiled, err)
	}
	if d := diagnostic(nil, "message"); d.Message != "message" || d.Row != 0 {
		t.Fatal(d)
	}
	if got := compileError(context.DeadlineExceeded); len(got.Diagnostics) != 0 || !errors.Is(got, context.DeadlineExceeded) {
		t.Fatal(got)
	}
	// A cached query reached under a caller deadline cannot continue on a
	// longer configured policy deadline.
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	if _, err := e.Evaluate(ctx, Request{Policy: Policy{Type: Plan, Engine: V1, Body: "package infraweave"}, Input: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("caller deadline ignored")
	}
}

func TestUnitProcessBudget(t *testing.T) {
	first, err := New(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if first.cache != second.cache {
		t.Fatal("separate consumers exceed the process query-cache budget")
	}
	for range Concurrency {
		first.slots <- struct{}{}
	}
	defer func() {
		for range Concurrency {
			<-first.slots
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	_, err = second.Evaluate(ctx, Request{Policy: Policy{Type: Plan, Engine: V1, Body: "package infraweave"}, Input: json.RawMessage(`{}`)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a ninth process evaluation started: %v", err)
	}
}

func TestUnitResultShapes(t *testing.T) {
	e := testEngine(t)
	for _, body := range []string{
		`package infraweave
inbox contains {"body":"notice","severity":"INFO","title":"title"} if true`,
		`package infraweave
slack contains {"channel_id":"channel"} if true`,
	} {
		evaluate(t, e, Policy{Type: Notification, Engine: V1, Body: body}, `{}`)
	}
	base := decision(t, evaluate(t, e, Policy{Type: Notification, Engine: V1, Body: "package infraweave"}, `{}`))
	base["inbox"] = []any{"bad"}
	if err := boundDecision(Notification, base); !errors.Is(err, ErrResult) {
		t.Fatal("non-object notification accepted")
	}
	base = decision(t, evaluate(t, e, Policy{Type: Plan, Engine: V1, Body: "package infraweave"}, `{}`))
	delete(base, "deny")
	base["unexpected"] = []any{}
	if err := boundDecision(Plan, base); !errors.Is(err, ErrResult) {
		t.Fatal("missing named output accepted")
	}
	for _, body := range []string{`package infraweave
roles := true`, `package infraweave
roles := ["admin"]`, `package infraweave
roles := {"root":["space-reader"]}`} {
		if err := e.Check(t.Context(), Policy{Type: Login, Engine: V1, Body: body}); err == nil {
			t.Fatal("invalid login roles rule kind saved")
		}
	}
	// A runtime conflict is returned as an evaluation fault, never as an allow.
	_, err := e.Evaluate(t.Context(), Request{Policy: Policy{Type: Login, Engine: V1, Body: `package infraweave
allow := true if input.conflict
allow := false if input.conflict`}, Input: json.RawMessage(`{"conflict":true}`)})
	if err == nil {
		t.Fatal("conflicting policy result accepted")
	}
	// Successful collections retain ordering as well as the concurrency bound.
	requests := []Request{
		{Policy: Policy{Type: Plan, Engine: V1, Body: `package infraweave
warn contains input.message if true`}, Input: json.RawMessage(`{"message":"first"}`)},
		{Policy: Policy{Type: Plan, Engine: V1, Body: `package infraweave
warn contains input.message if true`}, Input: json.RawMessage(`{"message":"second"}`)},
	}
	out := e.EvaluateAll(t.Context(), requests)
	for i, want := range []string{"first", "second"} {
		if out[i].Err != nil || decision(t, out[i].Result)["warn"].([]any)[0] != want {
			t.Fatalf("collection[%d] = %+v", i, out[i])
		}
	}
}
