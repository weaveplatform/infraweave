// Package webhooktest supplies a deterministic Webhooks provider for intake
// contracts. Production assembly never registers it; provider adapters supply
// their own verification and normalization capabilities.
//
// # Input and output
//
// Provider verifies a fixture's HMAC-SHA256 in constant time, uses safe GitHub
// event and delivery header names, consumes pushes and parses a normalized
// JSON Event. Tests can record calls and inject a parse refusal. Sign computes
// the retained digest; no credential or provider network call is involved.
//
// # Dependencies
//
// It imports standard-library cryptography, JSON and HTTP packages and the
// domain's api and core/port. Only tests import this fixture provider.
//
// ref: decisions 0012 and 0016
package webhooktest
