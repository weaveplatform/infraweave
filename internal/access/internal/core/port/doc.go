// Package port holds the access domain's ports: the interfaces its core
// needs from its adapters, and the errors those adapters return through
// them, so the core never imports an adapter (decision 0004). Tenancy,
// Sessions and APIKeys read and write tenancy, sessions and keys, and
// APIKeys a personal key's owner as the key's account admits them;
// BreakGlass reads an organization's break-glass credential and records
// its sign-ins; OrganizationKeys reads an organization key and records
// its use; OIDC verifies an issuer's JWT for an OIDC API key.
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
