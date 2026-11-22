package api

import "github.com/weaveplatform/infraweave/internal/kernel"

// EvaluatedKind and InternalErrorKind identify policy evaluation and internal
// error events. Notification routing owns delivery.
const (
	EvaluatedKind          = "policy.evaluated"
	InternalErrorKind      = "policy.internal_error"
	EvaluationEventVersion = 1
)

// Evaluated is metadata only. Decisions, input, source and print can contain
// secrets; they are deliberately absent from plaintext outbox documents.
type Evaluated struct {
	Evaluation    kernel.ULID  `json:"evaluation"`
	Policy        kernel.ULID  `json:"policy"`
	PolicyVersion int64        `json:"policyVersion"`
	Type          Type         `json:"type"`
	Engine        string       `json:"engineType"`
	Outcome       Outcome      `json:"outcome"`
	Target        Target       `json:"target"`
	Run           *kernel.ULID `json:"run,omitempty"`
	Failure       *Failure     `json:"failure,omitempty"`
}

// InternalError records safe policy failures and nonfatal combination conflicts.
type InternalError struct {
	Evaluation kernel.ULID  `json:"evaluation"`
	Type       Type         `json:"type"`
	Target     Target       `json:"target"`
	Run        *kernel.ULID `json:"run,omitempty"`
	Failure    Failure      `json:"failure"`
}

var EvaluatedSchema = []byte(`{
 "type":"object","additionalProperties":false,
 "required":["evaluation","policy","policyVersion","type","engineType","outcome","target"],
 "properties":{
 "evaluation":{"type":"string","minLength":26,"maxLength":26},
 "policy":{"type":"string","minLength":26,"maxLength":26},
 "policyVersion":{"type":"integer","minimum":1},
 "type":{"enum":["APPROVAL","GIT_PUSH","INITIALIZATION","INTENT","LOGIN","NOTIFICATION","PLAN","TASK","TRIGGER"]},
 "engineType":{"enum":["REGO_V0","REGO_V1"]},
 "outcome":{"enum":["ALLOW","DENY","APPROVE","REJECT","UNDECIDED","IGNORE","PUSH","TRIGGER","NOTIFY"]},
 "target":{"$ref":"#/$defs/target"},"run":{"type":"string","minLength":26,"maxLength":26},"failure":{"$ref":"#/$defs/failure"}},
 "$defs":{
 "target":{"type":"object","additionalProperties":false,"required":["kind","id"],"properties":{"kind":{"enum":["stack","space","module","intent_project"]},"id":{"type":"string","minLength":26,"maxLength":26}}},
 "failure":{"type":"object","additionalProperties":false,"required":["code","message"],"properties":{"code":{"type":"string"},"message":{"type":"string"}}}}
}`)

var InternalErrorSchema = []byte(`{
 "type":"object","additionalProperties":false,"required":["evaluation","type","target","failure"],
 "properties":{
 "evaluation":{"type":"string","minLength":26,"maxLength":26},"type":{"type":"string"},
 "target":{"type":"object","additionalProperties":false,"required":["kind","id"],"properties":{"kind":{"type":"string"},"id":{"type":"string","minLength":26,"maxLength":26}}},
 "run":{"type":"string","minLength":26,"maxLength":26},
 "failure":{"type":"object","additionalProperties":false,"required":["code","message"],"properties":{"code":{"type":"string"},"message":{"type":"string"}}}}
}`)
