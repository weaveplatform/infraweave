package rego

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type capturedExchange struct {
	Request struct {
		Variables struct {
			Input struct {
				Type   Type       `json:"type"`
				Engine EngineType `json:"engineType"`
				Body   string     `json:"body"`
				Input  string     `json:"input"`
			} `json:"input"`
		} `json:"variables"`
	} `json:"request"`
	Response struct {
		Data struct {
			Result string `json:"policySimulatev2"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	} `json:"response"`
}

func captured(t *testing.T, path string) []capturedExchange {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../../test/fixtures", path))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Exchanges []capturedExchange `json:"exchanges"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Exchanges) > 0 {
		return root.Exchanges
	}
	var one capturedExchange
	if err := json.Unmarshal(raw, &one); err != nil {
		t.Fatal(err)
	}
	return []capturedExchange{one}
}

func TestUnitRegoCapturedDecisions(t *testing.T) {
	e := testEngine(t)
	for _, path := range []string{"rego/preamble-simulations.json", "rego/login-role-forms.json", "policy-inputs/provenance/trigger-defaults.json"} {
		for _, exchange := range captured(t, path) {
			input := exchange.Request.Variables.Input
			body := strings.ReplaceAll(input.Body, "package spacelift", "package infraweave")
			body = strings.ReplaceAll(body, "input.spacelift", "input.infraweave")
			body = strings.ReplaceAll(body, "data.spacelift", "data.infraweave")
			p := Policy{Type: input.Type, Engine: input.Engine, Body: body}
			if len(exchange.Response.Errors) > 0 {
				if err := e.Check(t.Context(), p); err == nil {
					t.Fatalf("accepted a live-rejected %s rule form", input.Type)
				}
				continue
			}
			out, err := e.Evaluate(t.Context(), Request{Policy: p, Input: json.RawMessage(input.Input)})
			if err != nil {
				t.Fatalf("%s: %v", input.Type, err)
			}
			var want any
			if err := json.Unmarshal([]byte(exchange.Response.Data.Result), &want); err != nil {
				t.Fatal(err)
			}
			if got := decision(t, out); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s raw decision disagrees with real simulation\nwant %s\n got %s", input.Type, exchange.Response.Data.Result, out.Decision)
			}
		}
	}
}
