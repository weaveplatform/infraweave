// Package http is the runs domain's HTTP adapter: Server, the domain's
// partial of the API's strict server, which serves none of the domain's
// operations yet, so they answer 501, or answer from the stacks domain's
// partial where it serves them empty. Its tests hold internal/kernel's run
// states, run types, stack states and run priorities to the API's enums.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// None. The runs domain's assembly constructs it.
package http
