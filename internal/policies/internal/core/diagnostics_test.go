package core

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

func TestUnitPolicyDiagnosticUsesUnicodeColumns(t *testing.T) {
	runtime, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	body := "package infraweave\nwarn contains \"review\" if { \"é\" == \"é\"; missing }\n"
	svc := New(Deps{Runtime: runtime})
	_, err = svc.Create(t.Context(), kernel.Principal{}, Input{Name: "Bad", Body: body, Type: rego.Plan})
	var invalid *kernel.Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("expected compile refusal, got %v", err)
	}
	line := strings.Split(body, "\n")[1]
	prefix, _, found := strings.Cut(line, "missing")
	if !found {
		t.Fatal("fixture lacks the refused variable")
	}
	column := utf8.RuneCountInString(prefix) + 1
	for _, field := range invalid.Errors {
		if !strings.Contains(field.Message, "missing") {
			continue
		}
		want := kernel.Range{Start: kernel.Position{Line: 2, Column: column}, End: kernel.Position{Line: 2, Column: column + len("missing")}}
		if field.Pointer != "/body" || field.Range == nil || *field.Range != want {
			t.Fatalf("diagnostic range %+v, want %+v", field.Range, want)
		}
		return
	}
	t.Fatalf("no missing-variable diagnostic: %+v", invalid.Errors)
}

func TestUnitPolicyDiagnosticSpansMultipleLines(t *testing.T) {
	runtime, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	body := "package infraweave\nresult := {\n  \"é\": true\n}\n"
	svc := New(Deps{Runtime: runtime})
	_, err = svc.Create(t.Context(), kernel.Principal{}, Input{Name: "Bad", Body: body, Type: rego.Plan})
	var invalid *kernel.Invalid
	if !errors.As(err, &invalid) || len(invalid.Errors) != 1 {
		t.Fatalf("expected reserved-rule refusal, got %v", err)
	}
	want := kernel.Range{Start: kernel.Position{Line: 2, Column: 1}, End: kernel.Position{Line: 4, Column: 2}}
	if got := invalid.Errors[0].Range; got == nil || *got != want {
		t.Fatalf("multiline diagnostic range %+v, want %+v", got, want)
	}
}
