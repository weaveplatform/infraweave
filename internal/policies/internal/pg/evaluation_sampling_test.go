package pg_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccEvaluationSamplingHandoffIsPrivateAndIndependent(t *testing.T) {
	f := newFixture(t)
	body := `package infraweave
private_source := "private-source-marker"
deny contains "blocked" if { print("private-print-marker") }
flag contains "kept" if true
sample := true`
	if _, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "Sampling handoff", Type: rego.Plan, Body: body, Labels: []string{"autoattach:*"}}); err != nil {
		t.Fatal(err)
	}
	raw := evaluationInput(t, "plan-tracked-warn.json")
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var context map[string]json.RawMessage
	if err := json.Unmarshal(input["infraweave"], &context); err != nil {
		t.Fatal(err)
	}
	var run map[string]json.RawMessage
	if err := json.Unmarshal(context["run"], &run); err != nil {
		t.Fatal(err)
	}
	run["id"] = json.RawMessage(`"private-input-marker"`)
	var err error
	context["run"], err = json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	input["infraweave"], err = json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Evaluate(t.Context(), fixtureEvaluationRequest(f, policyapi.Plan, raw))
	if err != nil || got.Outcome != policyapi.Deny || len(got.Samples) != 1 || got.Samples[0].Body != body ||
		!bytes.Contains(got.Samples[0].Input, []byte("private-input-marker")) ||
		!strings.Contains(got.Samples[0].Print, "private-print-marker") {
		t.Fatalf("native sampling handoff missing: %+v %v", got, err)
	}
	serialized, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var events string
	if err := f.db.QueryRow(t.Context(), "SELECT string_agg(document::text,',') FROM events WHERE account_id=$1", f.principal.AccountID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"private-source-marker", "private-print-marker", "private-input-marker"} {
		if bytes.Contains(serialized, []byte(marker)) || strings.Contains(events, marker) {
			t.Fatalf("sampling data escaped into plaintext JSON/outbox: %s", marker)
		}
	}
	decision := bytes.Clone(got.Receipts[0].Decision)
	got.Samples[0].Receipt.Flags[0] = "changed by consumer"
	got.Samples[0].Receipt.Decision[0] = '!'
	if got.Receipts[0].Flags[0] != "kept" || !bytes.Equal(got.Receipts[0].Decision, decision) {
		t.Fatal("sampling consumer can mutate the workflow's receipt evidence")
	}
}

func TestAccEvaluationConfiguredInputLimitPrecedesDecode(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(
		t.Context(),
		f.principal,
		core.Input{Name: "Bounded input", Type: rego.Plan, Body: "package infraweave\n", Labels: []string{"autoattach:*"}},
	); err != nil {
		t.Fatal(err)
	}
	settings := rego.Defaults()
	settings.MaxInputBytes = 8
	runtime, err := rego.New(settings)
	if err != nil {
		t.Fatal(err)
	}
	f.runtime = runtime
	svc := f.service(f.events)
	for _, raw := range []json.RawMessage{json.RawMessage(`{"invalid":true}`), evaluationInput(t, "plan-tracked-warn.json")} {
		got, err := svc.Evaluate(t.Context(), fixtureEvaluationRequest(f, policyapi.Plan, raw))
		if err != nil || got.Outcome != policyapi.Deny || len(got.Failures) != 1 || got.Failures[0].Code != "LIMIT" {
			t.Fatalf("configured bound was applied after typed decode: %+v %v", got, err)
		}
	}
}
