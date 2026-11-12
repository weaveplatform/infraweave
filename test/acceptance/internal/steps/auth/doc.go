// Package auth holds the acceptance steps of features/auth: the token
// exchange and API sessions, API keys and personal keys, browser sign-in
// and its sessions, break-glass sign-in, the OAuth server and signing
// tokens with a KMS key.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk and the
// browser's cookie jar, seeds and reads their database through SQL, and
// creates Vault Transit keys for KMS signing.
//
// # Dependencies
//
// It builds on harness.World and harness.Jar, test/deps for Vault, and
// kernel for the IDs it seeds.
package auth
