// Package assembly constructs the expressions domain, the editor endpoints
// that check, complete and evaluate expressions over other domains'
// surfaces, last of all the domains. It imports no domain: internal/app
// hands it the registry of their surfaces. API is its partial of the
// API's strict server, aliased ExpressionsAPI, evaluating at the wall
// clock's time. The domain keeps no data, so it registers no migration
// stream.
//
// # Input and output
//
// New takes the expression registry.
//
// # Dependencies
//
// It imports the domain's http package and platform/expr. Only
// internal/app and cmd/ import it, as the constructors rule enforces.
package assembly
