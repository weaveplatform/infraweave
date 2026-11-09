// Package api is what other domains read of the configuration domain:
// Changed, the event the domain appends to the outbox whenever a context
// is created, changed, configured, attached, detached or deleted, with
// ChangedSchema its document's schema.
//
// # Input and output
//
// None; it declares types and a JSON Schema.
//
// # Dependencies
//
// It imports internal/kernel. The configuration domain's assembly
// registers the event's kind with the outbox.
package api
