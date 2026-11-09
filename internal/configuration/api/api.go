package api

import "github.com/weaveplatform/infraweave/internal/kernel"

// ChangedKind is the outbox event a context's write appends in its
// transaction, at ChangedVersion, with a Changed document. Its subject is
// the context's ULID and its sequence the context's version after the
// write, which every change to its elements and attachments moves on.
const (
	ChangedKind    = "context.changed"
	ChangedVersion = 1
)

// The changes a Changed event names.
const (
	ChangeCreated    = "created"
	ChangeUpdated    = "updated"
	ChangeConfigured = "configured"
	ChangeAttached   = "attached"
	ChangeDetached   = "detached"
	ChangeDeleted    = "deleted"
)

// Changed is a ChangedKind event's document: the context's ULID and slug,
// which change it was, and for an attachment or a detachment the stack's
// ULID.
type Changed struct {
	Context kernel.ULID  `json:"context"`
	Slug    string       `json:"slug"`
	Change  string       `json:"change"`
	Stack   *kernel.ULID `json:"stack,omitempty"`
}

// ChangedSchema is the JSON Schema of ChangedVersion's documents.
var ChangedSchema = []byte(`{
	"type": "object",
	"required": ["context", "slug", "change"],
	"properties": {
		"context": {"type": "string", "minLength": 26, "maxLength": 26},
		"slug": {"type": "string", "minLength": 1},
		"change": {"enum": ["created", "updated", "configured", "attached", "detached",
			"deleted"]},
		"stack": {"type": "string", "minLength": 26, "maxLength": 26}
	}
}`)
