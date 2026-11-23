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
// It answers the public pool as the caller's account uses it, each pool's
// queue counts and users, the operator's declared public egress, and the
// account's private worker usage cycles; the queue counts and users come
// from the runs and stacks domains through the ports Bind binds, and the
// quotas from the features domain.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports the access, features and workers api packages,
// internal/kernel and its search package, the generated API,
// platform/httpkit's apisearch, problem and strict packages and the
// domain's images, pool, sample and worker packages. The
// workers domain's assembly constructs it.
package http
