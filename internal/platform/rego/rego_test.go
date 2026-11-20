package rego

import (
	"context"
	"crypto/fips140"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-policy-agent/opa/v1/ast"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := New(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	engine.cache = newCache()
	engine.slots = make(chan struct{}, Concurrency)
	return engine
}

func evaluate(t *testing.T, e *Engine, p Policy, input string) Result {
	t.Helper()
	out, err := e.Evaluate(t.Context(), Request{Policy: p, Input: json.RawMessage(input)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func decision(t *testing.T, out Result) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(out.Decision, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestUnitRegoEngines(t *testing.T) {
	e := testEngine(t)
	for _, kind := range []Type{Access, Approval, Push, Initialization, Intent, Login, Notification, Plan, Task, Trigger} {
		for _, version := range []EngineType{V0, V1} {
			t.Run(string(kind)+"/"+string(version), func(t *testing.T) {
				out := evaluate(t, e, Policy{Type: kind, Engine: version, Body: "package infraweave"}, `{}`)
				value := decision(t, out)
				if len(value) != len(rules(kind)) {
					t.Fatalf("incomplete preamble: %s", out.Decision)
				}
				if got := value["sample"]; got != (kind == Login) {
					t.Fatalf("sample = %v for %s", got, kind)
				}
			})
		}
	}
	for _, tc := range []struct {
		version EngineType
		body    string
	}{
		{V0, "package infraweave\ndeny[msg] {msg := input.message}"},
		{V1, "package infraweave\ndeny contains msg if {msg := input.message}"},
		{V0, "package infraweave\nimport rego.v1\ndeny contains msg if {msg := input.message}"},
		{V0, "package infraweave\nimport future.keywords\ndeny contains msg if {msg := input.message}"},
	} {
		out := evaluate(t, e, Policy{Type: Plan, Engine: tc.version, Body: tc.body}, `{"message":"blocked"}`)
		if got := decision(t, out)["deny"].([]any); len(got) != 1 || got[0] != "blocked" {
			t.Fatal(string(out.Decision))
		}
	}
	for _, tc := range []struct {
		version EngineType
		body    string
	}{
		{V1, "package infraweave\ndeny[msg] {msg := \"no\"}"},
		{V0, "package infraweave\ndeny contains \"no\" if true"},
	} {
		if err := e.Check(t.Context(), Policy{Type: Plan, Engine: tc.version, Body: tc.body}); err == nil {
			t.Fatalf("dialect mismatch accepted: %s", tc.version)
		}
	}
	// A default built-in error leaves its expression undefined, rather than
	// failing the evaluation or adding a deny message.
	out := evaluate(t, e, Policy{Type: Plan, Engine: V1, Body: "package infraweave\ndeny contains \"error\" if {1 / input.divisor == 1}"}, `{"divisor":0}`)
	if len(decision(t, out)["deny"].([]any)) != 0 {
		t.Fatal(string(out.Decision))
	}
}

func TestUnitRemovedBuiltins(t *testing.T) {
	e := testEngine(t)
	for name, call := range map[string]string{
		"http.send":          `http.send({"method":"GET","url":"https://example.invalid"})`,
		"net.lookup_ip_addr": `net.lookup_ip_addr("example.invalid")`,
		"opa.runtime":        `opa.runtime()`, "rego.parse_module": `rego.parse_module("x","package x")`,
		"time.now_ns": `time.now_ns()`, "trace": `trace("secret")`, "exec": `exec(["true"])`,
	} {
		t.Run(name, func(t *testing.T) {
			err := e.Check(t.Context(), Policy{Type: Plan, Engine: V1, Body: "package infraweave\ndeny contains sprintf(\"%v\", [" + call + "]) if true"})
			var compiled *CompileError
			if !errors.As(err, &compiled) || len(compiled.Diagnostics) == 0 {
				t.Fatalf("removed builtin accepted or lost diagnostics: %v", err)
			}
		})
	}
	// Exercise FIPS save-time rejection even in an ordinary developer process.
	e.capabilities = func() *ast.Capabilities { return capabilitiesFor(true) }
	for name, args := range map[string]string{"crypto.md5": `"a"`, "crypto.sha1": `"a"`, "crypto.hmac.md5": `"a","b"`, "crypto.hmac.sha1": `"a","b"`} {
		if err := e.Check(t.Context(), Policy{Type: Plan, Engine: V1, Body: "package infraweave\ndeny contains " + name + "(" + args + ") if true"}); err == nil {
			t.Errorf("FIPS saved %s", name)
		}
	}
	if fips140.Enforced() {
		for _, builtin := range capabilities().Builtins {
			if builtin.Name == "crypto.md5" {
				t.Fatal("enforcement mode retains MD5")
			}
		}
	}
	for _, body := range []string{
		"package other", "package infraweave\nimport rego.v2", "package infraweave\nresult := {}",
		"package infraweave\ndeny := true", "package infraweave\ndefault allow := false",
	} {
		kind := Plan
		if strings.Contains(body, "default allow") {
			kind = Login
		}
		if err := e.Check(t.Context(), Policy{Type: kind, Engine: V1, Body: body}); err == nil {
			t.Errorf("invalid package/import/rule kind accepted: %s", body)
		}
	}
}

func TestUnitSanitizedAndPrint(t *testing.T) {
	e := testEngine(t)
	p := Policy{Type: Plan, Engine: V1, Body: `package infraweave
warn contains sanitized("hello") if true
info contains sprintf("%v", [sanitized(input.object)]) if true
deny contains "no" if { print(input.secret); false }
`}
	out := evaluate(t, e, p, `{"secret":"private input","object":{"plain":"unchanged"}}`)
	value := decision(t, out)
	if value["warn"].([]any)[0] != "938b9824" || !strings.Contains(value["info"].([]any)[0].(string), "unchanged") || out.Print != "private input\n" {
		t.Fatalf("sanitized/print mismatch: %+v %s", out, out.Decision)
	}
	out = evaluate(t, e, Policy{Type: Plan, Engine: V1, Body: `package infraweave
deny contains "no" if { print(input.text); false }`}, `{"text":"`+strings.Repeat("世", PrintBytes)+`"}`)
	if len(out.Print) > PrintBytes || !out.PrintTruncated {
		t.Fatalf("print bound = %d, truncated %v", len(out.Print), out.PrintTruncated)
	}
	if strings.Contains(string(out.Decision), "世") {
		t.Fatal("print leaked into decision")
	}
	// A later evaluation of the same prepared query has a separate hook.
	clean := evaluate(t, e, p, `{"secret":"next","object":null}`)
	if clean.Print != "next\n" || strings.Contains(clean.Print, "private") {
		t.Fatal("print buffer leaked across cached evaluations")
	}
}

func TestUnitPolicyTimeout(t *testing.T) {
	settings := Defaults()
	settings.Timeouts.Combined = 40 * time.Millisecond
	e, err := New(settings)
	if err != nil {
		t.Fatal(err)
	}
	p := Policy{Type: Plan, Engine: V1, Body: `package infraweave
deny contains "late" if {
 some a in input.values
 some b in input.values
 some c in input.values
 some d in input.values
 a+b+c+d == -1
}`}
	values := make([]int, 300)
	for i := range values {
		values[i] = i
	}
	input, err := json.Marshal(map[string]any{"values": values})
	if err != nil {
		t.Fatal(err)
	}
	// Prepare before timing the expensive evaluation; it still uses the real
	// compiler and the same per-type deadline as a save.
	if err := e.Check(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	e.settings.Timeouts.Plan = 25 * time.Millisecond
	started := time.Now()
	if _, err := e.Evaluate(t.Context(), Request{Policy: p, Input: input}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expensive evaluation = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancellation did not stop expensive evaluation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := e.Check(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled compile = %v", err)
	}
	requests := make([]Request, 20)
	for i := range requests {
		requests[i] = Request{Policy: p, Input: input}
	}
	for _, out := range e.EvaluateAll(t.Context(), requests) {
		if out.Err == nil {
			t.Fatal("combined timeout produced a successful expensive result")
		}
	}
	if got := e.EvaluateAll(t.Context(), nil); len(got) != 0 {
		t.Fatal(got)
	}
	// A queue wait respects cancellation without beginning an evaluation.
	for range Concurrency {
		e.slots <- struct{}{}
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, err := e.Evaluate(ctx, requests[0]); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued cancellation = %v", err)
	}
	for range Concurrency {
		<-e.slots
	}
}

func TestUnitPreparedIsolation(t *testing.T) {
	e := testEngine(t)
	var wg sync.WaitGroup
	for i := range 80 {
		wg.Go(func() {
			engine, rule := V1, "deny contains message if { print(message) }"
			if i%4 >= 2 {
				engine, rule = V0, "deny[message] { print(message) }"
			}
			body := fmt.Sprintf("package infraweave\nhelper := %d\nmessage := sprintf(\"%%v-%%v\", [helper,input.id])\n%s", i%2, rule)
			out, err := e.Evaluate(t.Context(), Request{Policy: Policy{Type: Plan, Engine: engine, Body: body}, Input: fmt.Appendf(nil, `{"id":%d}`, i)})
			if err != nil {
				t.Error(err)
				return
			}
			var value map[string][]string
			if json.Unmarshal(out.Decision, &value) == nil {
				t.Error("boolean fields disappeared")
				return
			}
			got := decision(t, out)["deny"].([]any)
			want := fmt.Sprintf("%d-%d", i%2, i)
			if len(got) != 1 || got[0] != want || out.Print != want+"\n" || out.PrintTruncated {
				t.Errorf("cached helper/input/print leak: %s", out.Decision)
			}
		})
	}
	wg.Wait()
	if e.cache.order.Len() != 4 {
		t.Fatalf("cache holds %d queries for two bodies in each dialect", e.cache.order.Len())
	}
	e.cache = newCache()
	e.cache.limit = 2
	for _, kind := range []Type{Plan, Trigger, Login} {
		evaluate(t, e, Policy{Type: kind, Engine: V1, Body: "package infraweave"}, `{}`)
	}
	if e.cache.order.Len() != 2 || len(e.cache.entries) != 2 {
		t.Fatal("LRU exceeded capacity")
	}
	// Effective capabilities are part of the key, even on the same Engine.
	e = testEngine(t)
	body := "package infraweave\ndeny contains crypto.md5(input.value) if true"
	p := Policy{Type: Plan, Engine: V1, Body: body}
	if !fips140.Enforced() {
		e.capabilities = func() *ast.Capabilities { return capabilitiesFor(false) }
		if err := e.Check(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	e.capabilities = func() *ast.Capabilities { return capabilitiesFor(true) }
	if err := e.Check(t.Context(), p); err == nil {
		t.Fatal("unsafe query reused after capability change")
	}
}
