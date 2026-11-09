// Package http is the configuration domain's HTTP adapter: Server, the
// domain's partial of the API's strict server, which serves the contexts,
// their config elements and their attachments to stacks, and the searches
// of the contexts a caller reads and of those a stack in a space may
// attach. It answers each context with the actions the caller holds in its
// space, and never answers a write-only value.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports the domain's core package, the access and stacks api
// packages, internal/kernel and its search package, the generated API,
// and platform/httpkit's apisearch, problem and strict packages. The
// configuration domain's assembly constructs it.
package http
