package inputs

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestUnitInputValidationUsesPublishedContract(t *testing.T) {
	for _, test := range []struct{ kind, file string }{
		{"PLAN", "plan-tracked-warn.json"},
		{"PLAN", "plan-pulumi.json"},
		{"PLAN", "plan-ansible.json"},
		{"PLAN", "plan-c53-capture.json"},
		{"APPROVAL", "approval-c51-queued.json"},
		{"APPROVAL", "approval-c51-unconfirmed.json"},
		{"APPROVAL", "approval-c51-reset-after.json"},
		{"GIT_PUSH", "push-track.json"},
		{"GIT_PUSH", "push-pull-request-opened.json"},
		{"TRIGGER", "trigger-finished.json"},
		{"NOTIFICATION", "notification-finished.json"},
	} {
		t.Run(test.file, func(t *testing.T) {
			if err := Validate(test.kind, fixture(t, test.file)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []string{`{}`, `[]`, `null`, `{"private":"secret-marker"}`, `{} {}`, `{"request":null}`} {
		if err := Validate("TASK", json.RawMessage(raw)); !errors.Is(err, ErrInput) || strings.Contains(err.Error(), "secret-marker") {
			t.Fatalf("unsafe/missing contract failure: %v", err)
		}
	}
	for _, kind := range []string{"ACCESS", "STACK_ACCESS", "unknown"} {
		if err := Validate(kind, json.RawMessage(`{}`)); !errors.Is(err, ErrInput) {
			t.Fatal(err)
		}
	}
	if err := Validate("TASK", bytes.Repeat([]byte(" "), MaxDocumentBytes+1)); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}
