// Package webhookcheck compares provider hook listings with the URL and
// events that an integration needs, with bounded, credential-free diagnostics.
//
// # Input and output
//
// Check takes the repositories used by an integration, its delivery URL,
// consumed events and a provider hook-listing function. It returns a WEBHOOK
// check, naming at most ten failing repositories and counting the remainder.
//
// # Dependencies
//
// It imports the standard library and the VCS domain's port contracts.
package webhookcheck
