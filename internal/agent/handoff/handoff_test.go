package handoff_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

func TestUnitHandoffRoundTrip(t *testing.T) {
	h := handoff.Handoff{
		Endpoint: "https://iw.example.com/worker/v1", Run: "run-1", RunToken: "token",
		Assigned: wire.AssignmentStatePREPARINGREPLAN, StateVersion: 9,
		Secrets: wire.RunSpecSecrets{ApiToken: "api", Files: map[string][]byte{"a": []byte("b")}},
	}
	var b bytes.Buffer
	if err := handoff.Write(&b, h); err != nil {
		t.Fatal(err)
	}
	got, err := handoff.Read(&b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Run != h.Run || got.StateVersion != 9 || got.Secrets.ApiToken != "api" ||
		string(got.Secrets.Files["a"]) != "b" || got.First() != wire.WorkerStatePLANNING {
		t.Fatalf("Read: %+v", got)
	}
}

func TestUnitHandoffFirst(t *testing.T) {
	for assigned, want := range map[wire.AssignmentState]wire.WorkerState{
		wire.AssignmentStatePREPARING:       wire.WorkerStateINITIALIZING,
		wire.AssignmentStatePREPARINGREPLAN: wire.WorkerStatePLANNING,
		wire.AssignmentStatePREPARINGAPPLY:  wire.WorkerStateAPPLYING,
		"QUEUED":                            "",
	} {
		if got := (handoff.Handoff{Assigned: assigned}).First(); got != want {
			t.Errorf("First after %s = %q, want %q", assigned, got, want)
		}
	}
}

func TestUnitHandoffRefuses(t *testing.T) {
	for name, in := range map[string]string{
		"empty":         "",
		"not JSON":      "{",
		"unknown field": `{"run":"r","extra":1}`,
		"no run token":  `{"endpoint":"e","run":"r","assigned":"PREPARING"}`,
		"a bad state":   `{"endpoint":"e","run":"r","runToken":"t","assigned":"QUEUED"}`,
		"too long":      `{"endpoint":"` + strings.Repeat("e", handoff.MaxSize) + `"}`,
	} {
		if _, err := handoff.Read(strings.NewReader(in)); !errors.Is(err, handoff.ErrHandoff) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := handoff.Write(failing{}, handoff.Handoff{}); err == nil {
		t.Fatal("Write to a failing writer")
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errors.New("closed") }
