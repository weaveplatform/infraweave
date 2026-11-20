// Package evidence defines runs-owned receipts and policy-owned flags.
//
// ref: decision 0009
//
// # Input and output
//
// Store accepts independent policy receipts under an account/run state fence
// and returns committed metadata and the visible union of flag ownership.
// Read projections exclude raw decisions, policy source, inputs and print.
//
// # Effects
//
// Writes join the caller's transition transaction. Reset removes only its
// policy's rows before additions; state, history, events and queue completion
// commit or roll back with the receipt and flag changes.
//
// # Dependencies
//
// The kernel and public policies API; storage remains in the runs adapter.
package evidence
