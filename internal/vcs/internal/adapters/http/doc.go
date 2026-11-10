// Package http is the vcs domain's HTTP adapter: Server, the domain's
// partial of the API's strict server, which serves none of the domain's
// operations yet, so they answer 501.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// None. The vcs domain's assembly constructs it.
package http
