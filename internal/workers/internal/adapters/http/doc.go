// Package http is the workers domain's HTTP adapter: Server, the domain's
// partial of the API's strict server. It lists, reads, searches and
// suggests the private worker pools of the caller's account in the spaces
// it may read, and the pools a stack, or a stack in a space, may name;
// creates one from a certificate signing request and answers it with its
// pool token; replaces or merge-patches its settings; resets its
// certificate from a new request; and deletes it, answering it as it
// was. Each pool answers the actions its caller holds in its space, its
// effective preparing timeout and its workers. It drains and undrains a
// worker, cycles a pool's workers, and searches and suggests a pool's
// workers. It reads and replaces the account's default runner images.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports the access api package, internal/kernel and its search
// package, the generated API, platform/httpkit's apisearch, problem and
// strict packages and the domain's images, pool and worker packages. The
// workers domain's assembly constructs it.
package http
