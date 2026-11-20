// Package api is the policies domain's public event contract. Changed identifies
// a committed policy save or deletion without retaining its source text.
//
// # Input and output
//
// None; this package declares event types and their JSON Schema.
//
// # Dependencies
//
// internal/kernel. The policies assembly registers the event kind with the outbox.
package api
