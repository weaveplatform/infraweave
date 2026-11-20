package rego

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

func TestUnitPrintNeverLogs(t *testing.T) {
	const marker = "private-policy-print-641-640"
	const child = "INFRAWEAVE_TEST_REGO_PRINT_CHILD"
	if os.Getenv(child) == "1" {
		e := testEngine(t)
		out, err := e.Evaluate(t.Context(), Request{Policy: Policy{Type: Plan, Engine: V1, Body: `package infraweave
warn contains "retained" if { print(input.secret) }`}, Input: json.RawMessage(`{"secret":"` + marker + `"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if out.Print != marker+"\n" {
			t.Fatal("print was not captured")
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestUnitPrintNeverLogs$", "-test.timeout=10s")
	command.Env = append(os.Environ(), child+"=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime child failed: %v", err)
	}
	if bytes.Contains(output, []byte(marker)) {
		t.Fatal("policy input or print leaked to process output")
	}
}
