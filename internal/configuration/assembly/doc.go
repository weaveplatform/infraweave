// Package assembly constructs the configuration domain, the contexts and
// config elements that stacks read, after stacks. API is its partial of
// the API's strict server, aliased ConfigurationAPI; Stream is its
// migration stream, which internal/app registers after stacks'.
//
// # Input and output
//
// None.
//
// # Dependencies
//
// It imports the domain's http and pg packages and platform/sql/migrate.
// Only internal/app and cmd/ import it, as the constructors rule enforces.
package assembly
