// Package http is the vcs domain's HTTP adapter: Server, the domain's
// partial of the API's strict server. It lists, reads and searches the VCS
// integrations of the caller's account in the spaces it may read, lists
// those of a provider a stack in a space may bind, and tests one through
// its provider's Tester, answering 501 for a provider with none. An
// integration's provider details come from its provider's Describe, and
// only to a caller with SPACE_ADMIN in its space. The provider operations
// that create and change integrations answer 501 until their providers
// serve them.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports the access api package, internal/kernel and its search
// package, the generated API, platform/httpkit's apisearch and problem
// packages and the domain's integration and port packages. The vcs
// domain's assembly constructs it.
package http
