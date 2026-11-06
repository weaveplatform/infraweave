// Package port holds the access domain's ports: the interfaces its core
// needs from its adapters, and the errors those adapters return through
// them, so the core never imports an adapter (decision 0004). OIDC
// verifies an issuer's JWT for an OIDC API key.
//
// # Input and output
//
// None; it declares interfaces and errors.
//
// # Dependencies
//
// It imports the access domain's core packages whose values cross a port,
// internal/kernel and the standard library. The core and the adapters that
// implement it import it.
package port
