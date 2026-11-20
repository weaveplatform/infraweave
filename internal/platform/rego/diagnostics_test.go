package rego

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitDiagnosticSourceByteRanges(t *testing.T) {
	body := "package infraweave\nwarn contains \"é🦉\" if {\n  missing\n}\n"
	unicode := strings.Index(body, "é")
	for _, tc := range []struct {
		name string
		d    Diagnostic
		want *kernel.Range
	}{
		{"offset overrides point", Diagnostic{File: "policy.rego", Offset: unicode, Length: 6, Row: 999, Column: 999}, &kernel.Range{Start: kernel.Position{Line: 2, Column: 16}, End: kernel.Position{Line: 2, Column: 18}}},
		{"multiline", Diagnostic{File: "policy.rego", Offset: strings.Index(body, "warn"), Length: len(body) - 20}, &kernel.Range{Start: kernel.Position{Line: 2, Column: 1}, End: kernel.Position{Line: 4, Column: 2}}},
		{"end of file", Diagnostic{File: "policy.rego", Offset: len(body)}, &kernel.Range{Start: kernel.Position{Line: 5, Column: 1}, End: kernel.Position{Line: 5, Column: 1}}},
		{"clamped span", Diagnostic{File: "policy.rego", Offset: len(body), Length: 50}, &kernel.Range{Start: kernel.Position{Line: 5, Column: 1}, End: kernel.Position{Line: 5, Column: 1}}},
		{"internal preamble", Diagnostic{File: "preamble.rego", Offset: 18, Length: 10}, nil},
		{"unlocated", Diagnostic{}, nil},
		{"negative offset", Diagnostic{File: "policy.rego", Offset: -1}, nil},
		{"outside source", Diagnostic{File: "policy.rego", Offset: len(body) + 1}, nil},
		{"negative length", Diagnostic{File: "policy.rego", Length: -1}, nil},
		{"partial rune start", Diagnostic{File: "policy.rego", Offset: unicode + 1, Length: 1}, nil},
		{"partial rune end", Diagnostic{File: "policy.rego", Offset: unicode, Length: 1}, &kernel.Range{Start: kernel.Position{Line: 2, Column: 16}, End: kernel.Position{Line: 2, Column: 17}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.d.SourceRange(body)
			if got == nil || tc.want == nil {
				if got != tc.want {
					t.Fatalf("range %+v, want %+v", got, tc.want)
				}
			} else if *got != *tc.want {
				t.Fatalf("range %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUnitRecordedOPACheckDiagnostics(t *testing.T) {
	e := testEngine(t)
	root := "../../../test/fixtures/vendor-documents/opa"
	for _, tc := range []struct {
		name, source, result string
		engine               EngineType
	}{
		{"parser", "broken", "check-broken.json", V1},
		{"type checker", "undefined", "check-undefined.json", V1},
		{"V0 source under V1", "v0only", "check-v0only.json", V1},
		{"V1 source under V0", "v1only", "check-v1only-v0-compatible.json", V0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, "check", tc.source+".rego"))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(root, tc.result))
			if err != nil {
				t.Fatal(err)
			}
			var recorded struct {
				Errors []struct {
					Message  string `json:"message"`
					Location struct {
						Row int `json:"row"`
						Col int `json:"col"`
					} `json:"location"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(raw, &recorded); err != nil || len(recorded.Errors) == 0 {
				t.Fatalf("recorded check contains no errors: %v", err)
			}
			source := strings.ReplaceAll(string(body), "spacelift", "infraweave")
			err = e.Check(t.Context(), Policy{Type: Plan, Engine: tc.engine, Body: source})
			var compile *CompileError
			if !errors.As(err, &compile) {
				t.Fatalf("recorded-invalid source accepted: %v", err)
			}
			for _, expected := range recorded.Errors {
				found := false
				for _, d := range compile.Diagnostics {
					if d.Message != expected.Message {
						continue
					}
					r := d.SourceRange(source)
					if r != nil && r.Start.Line == expected.Location.Row && r.Start.Column == expected.Location.Col {
						found = true
					}
				}
				if !found {
					t.Fatalf("lost recorded diagnostic %+v: %+v", expected, compile.Diagnostics)
				}
			}
		})
	}
}
