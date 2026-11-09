// Package assembly constructs the expressions domain, the editor endpoints
// that check, complete and evaluate expressions over other domains'
// surfaces, last of all the domains. It imports no domain: internal/app
// hands it what it reads of them. API is its partial of the API's strict
// server, aliased ExpressionsAPI. The domain keeps no data, so it
// registers no migration stream.
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
