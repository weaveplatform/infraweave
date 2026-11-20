package api

import "github.com/weaveplatform/infraweave/internal/kernel"

// ChangedKind names a committed policy change. Its subject is the immutable
// policy ID and its sequence is the policy's version after that change.
const (
	ChangedKind    = "policy.changed"
	ChangedVersion = 1
)

// The management changes a Changed event records.
const (
	ChangeCreated  = "created"
	ChangeUpdated  = "updated"
	ChangeDeleted  = "deleted"
	ChangeAttached = "attached"
	ChangeDetached = "detached"
)

// Changed identifies a policy change without copying its source into the outbox.
type Changed struct {
	Policy kernel.ULID `json:"policy"`
	Change string      `json:"change"`
}

// ChangedSchema validates the public version-one event document.
var ChangedSchema = []byte(`{
  "type":"object", "additionalProperties":false,
  "required":["policy","change"],
  "properties":{
    "policy":{"type":"string","minLength":26,"maxLength":26},
    "change":{"enum":["created","updated","deleted","attached","detached"]}
  }
}`)
