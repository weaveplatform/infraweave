package kernel

import (
	"encoding/json"
	"time"
)

// AuditEntry is one entry of an account's audit trail, decision 0015's
// AuditTrailEntry: who changed what, recorded in the transaction of the
// change.
type AuditEntry struct {
	AccountID ULID
	// ID is assigned when the entry is recorded.
	ID    ULID
	Actor AuditActor
	// Action is <resource>.<verb> in snake case, such as stack.create.
	Action    string
	EventType string
	// AffectedResource is what the change acted on; RelatedResource, when
	// set, is what it acted through, such as the stack of a run.
	AffectedResource AuditResource
	RelatedResource  *AuditResource
	// Body is the request as JSON, or nil.
	Body         json.RawMessage
	RemoteIP     string
	IdentityULID ULID
	OwnerLogin   string
	// CreatedAt is set when the entry is recorded, unless the caller set it.
	CreatedAt time.Time
}

// AuditActor is who made an audited change.
type AuditActor struct {
	Username string
	// Run is the administrative run that acted, with username infraweave;
	// the zero ULID otherwise.
	Run ULID
}

// AuditResource is a resource an audited change touched.
type AuditResource struct {
	// Type is an AuditTrailResourceType, such as STACK.
	Type string
	ID   string
	// Metadata is the type's own fields as JSON, or nil.
	Metadata json.RawMessage
}
