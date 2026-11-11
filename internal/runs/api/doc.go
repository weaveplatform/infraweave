// Package api is what other domains read of the runs domain: Updated, the
// event the domain appends to the outbox each time a run changes state,
// with UpdatedSchema its document's schema, and RunSources, what a run's
// end asks of the vcs domain, a back edge that domain implements.
//
// # Input and output
//
// None; it declares types, an interface and a JSON Schema.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The domains above
// runs in the declared graph import it, and the vcs domain implements
// RunSources.
package api
