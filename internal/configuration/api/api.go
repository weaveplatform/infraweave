package api

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

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

// Surface is the expression surface of a stack's environment: env, the
// environment variables a run of the stack resolves to, by name, and
// stack, the stack's fields. A value's ${{ … }} templates are checked
// against it when the value is saved, and rendered against it when a run
// is handed over; ${NAME} is left to the shell.
//
// ref: decision 0024 "Surfaces and their variables"
const Surface = "environment"

// The sources an element or a hook of a stack's environment comes from.
const (
	SourceStack        = "stack"
	SourceContext      = "context"
	SourceAutoattached = "autoattached"
)

// ErrNoStack reports a stack the account lacks.
var ErrNoStack = errors.New("configuration: no such stack")

// Secret is an opened value; Reveal returns its bytes. The configuration
// domain's are values that render as [redacted] everywhere else.
type Secret interface{ Reveal() []byte }

// Element is one config element of a stack's environment, from Source,
// and from the context Context names when it is a context's. Value holds
// the value as saved, its templates unrendered, whether or not it is
// write-only.
type Element struct {
	Name      string
	FileMount bool
	WriteOnly bool
	FileMode  *string
	Checksum  string
	Value     Secret
	Source    string
	Context   string
}

// Resolved is a stack's environment: the elements that win, by name, and
// each hook phase's commands in the order they run.
type Resolved struct {
	Elements []Element
	Hooks    map[string][]string
}

// Environment resolves a stack's environment for a run's spec, the one
// place values are opened.
type Environment interface {
	// Resolve resolves acct's stack id: an element of the stack's own
	// outranks one of an attached context, a context attached by hand one
	// attached automatically, and among those attached by hand the lower
	// priority wins, among those attached automatically the first by
	// name. Before a phase the hooks of the contexts attached by hand run
	// first, by priority, then those of the contexts attached
	// automatically, last name first, then the stack's; after a phase they
	// run in the reverse order, so the outermost wraps the phase. ErrNoStack
	// when acct lacks the stack.
	Resolve(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Resolved, error)
}
