// Package http is the expressions domain's HTTP adapter: Server, the
// domain's partial of the API's strict server, which serves the editor
// endpoints over every surface the registry holds. Each finds its surface
// by name, refusing an unknown one at /surface, and its context through
// the surface's Env, answering 404 for one the caller cannot read. Check
// answers diagnostics with their ranges, the result's type and its cost;
// complete, the items at a cursor; evaluate, the result as JSON, against
// the context's sample values with the request's in their place, every
// secret key read as the mask, and a source that does not type-check or
// an evaluation that fails refused at /source with the fault's range.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports internal/kernel, the generated API, and platform's expr,
// httpkit/problem and mask packages. The expressions domain's assembly
// constructs it.
package http
