// Package http is the workers domain's HTTP adapter: Server, the domain's
// partial of the API's strict server. It lists, reads, searches and
// suggests the private worker pools of the caller's account in the spaces
// it may read; creates one from a certificate signing request and answers
// it with its pool token; replaces or merge-patches its settings; resets
// its certificate from a new request; and deletes it, answering it as it
// was. Each pool answers the actions its caller holds in its space and
// its effective preparing timeout.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports the access api package, internal/kernel and its search
// package, the generated API, platform/httpkit's apisearch, problem and
// strict packages and the domain's pool package. The workers domain's
// assembly constructs it.
package http
