// Package assembly constructs the vendors domain, the tool version
// catalogue, after access and ahead of stacks. API is its partial of the
// API's strict server, aliased VendorsAPI. The domain has no tables yet,
// so it registers no migration stream.
//
// # Input and output
//
// None.
//
// # Dependencies
//
// It imports the domain's http package. Only internal/app and cmd/ import
// it, as the constructors rule enforces.
package assembly
