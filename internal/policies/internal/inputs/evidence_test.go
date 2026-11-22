package inputs

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestUnitEvidenceFactsKeepFalseSeparateFromUnknownAndExcludePrivateValues(t *testing.T) {
	for _, test := range []struct {
		name, kind, raw string
		want            EvaluationFacts
	}{
		{name: "plan false", kind: "PLAN", raw: `{"infraweave":{"run":{"type":"TRACKED","drift_detection":false,"command":"private-input"},"stack":{"name":"Original","repository":"repo"}},"terraform":{"private":"private-input"}}`, want: EvaluationFacts{RunType: new("TRACKED"), RunDriftDetection: new(false), StackName: new("Original"), StackRepository: new("repo")}},
		{name: "plan unknown", kind: "PLAN", raw: `{"infraweave":{"run":{"type":"PROPOSED"},"stack":{}}}`, want: EvaluationFacts{RunType: new("PROPOSED")}},
		{name: "plan null", kind: "PLAN", raw: `{"infraweave":{"run":{"drift_detection":null}}}`},
		{name: "push branch", kind: "GIT_PUSH", raw: `{"push":{"branch":"main"},"stack":{"name":"Stack","repository":"repo"}}`, want: EvaluationFacts{PushBranch: new("main"), StackName: new("Stack"), StackRepository: new("repo")}},
		{name: "pull request", kind: "GIT_PUSH", raw: `{"push":{"branch":"base"},"pull_request":{"id":47,"head":{"branch":"feature"},"title":"private-input"}}`, want: EvaluationFacts{PullRequestID: new(int64(47)), PushBranch: new("feature")}},
		{name: "notification run", kind: "NOTIFICATION", raw: `{"run_updated":{"run":{"type":"TASK","drift_detection":false},"stack":{"name":"Stack","repository":"repo"}}}`, want: EvaluationFacts{RunType: new("TASK"), RunDriftDetection: new(false), StackName: new("Stack"), StackRepository: new("repo"), ModuleEvent: new(false)}},
		{name: "notification module", kind: "NOTIFICATION", raw: `{"module_version":{"module":{"id":"module-a"}}}`, want: EvaluationFacts{ModuleID: new("module-a"), ModuleEvent: new(true)}},
		{name: "notification other", kind: "NOTIFICATION", raw: `{"internal_error":{"error":"private-input"}}`},
		{name: "unavailable document", kind: "PLAN"},
		{name: "other kind", kind: "LOGIN", raw: `{"session":{"login":"private-input"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(test.raw)
			before := bytes.Clone(raw)
			got, err := Facts(test.kind, raw)
			if err != nil || !reflect.DeepEqual(got, test.want) || !bytes.Equal(raw, before) {
				t.Fatalf("metadata snapshot differs: %+v %v", got, err)
			}
		})
	}
	if _, err := Facts("PLAN", json.RawMessage(`{"private-input":`)); !errors.Is(err, ErrInput) {
		t.Fatalf("invalid metadata document accepted: %v", err)
	}
}
