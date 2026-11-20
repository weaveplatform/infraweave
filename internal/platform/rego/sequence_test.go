package rego

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestUnitPolicySequenceUsesIndependentInputsAndErrors(t *testing.T) {
	e := testEngine(t)
	if got := e.EvaluateSequence(t.Context(), nil, func(int, Outcome) (json.RawMessage, error) { t.Fatal("empty sequence advanced"); return nil, nil }); len(got) != 0 {
		t.Fatal(got)
	}
	policy := Policy{Type: Login, Engine: V1, Body: "package infraweave\nallow if input.allowed"}
	requests := []Request{{Policy: policy, Input: json.RawMessage(`{"allowed":false}`)}, {Policy: policy, Input: json.RawMessage(`{"allowed":false}`)}}
	out := e.EvaluateSequence(t.Context(), requests, func(_ int, result Outcome) (json.RawMessage, error) {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		return json.RawMessage(`{"allowed":true}`), nil
	})
	if decision(t, out[0].Result)["allow"] != false || decision(t, out[1].Result)["allow"] != true || string(requests[1].Input) != `{"allowed":false}` {
		t.Fatal("sequence leaked/missed the controlled input change")
	}
	bad := errors.New("adapter failure")
	out = e.EvaluateSequence(t.Context(), requests, func(int, Outcome) (json.RawMessage, error) { return nil, bad })
	for _, result := range out {
		if !errors.Is(result.Err, bad) {
			t.Fatalf("lost adapter failure: %v", result.Err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out = e.EvaluateSequence(ctx, requests, func(_ int, result Outcome) (json.RawMessage, error) { return nil, result.Err })
	for _, result := range out {
		if !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("canceled sequence evaluated: %v", result.Err)
		}
	}
}
