// Package webhookauth checks the shared SHA-256 signature protocol used by
// Bitbucket Cloud and Data Center without interpreting delivery bodies.
//
// # Input and output
//
// Verify accepts the provider's signature header name, exact body and active
// secrets. It compares every policy-permitted candidate in constant time and
// returns the exact verified header, including its algorithm prefix. Missing,
// ambiguous, invalid or policy-ineligible signatures produce ErrSignature.
// The caller selects any previous secret still inside its rotation window.
//
// # Dependencies
//
// It imports only the standard library. Provider adapters call it; it has no
// persistence, delivery lifecycle, event or provider-network dependency.
package webhookauth
