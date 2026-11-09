// Package api is what other domains read of the configuration domain:
// Changed, the event the domain appends to the outbox whenever a context
// is created, changed, configured, attached, detached or deleted, with
// ChangedSchema its document's schema; Environment, which resolves a
// stack's ordered environment and hooks for a run as Resolved, with each
// Element's opened value a Secret; and Surface, the name of the
// expression surface its values' templates are checked and rendered
// against.
//
// # Input and output
//
// None; it declares types, an interface and a JSON Schema.
//
// # Dependencies
//
// It imports internal/kernel. The configuration domain's assembly
// registers the event's kind with the outbox.
package api
