// Package api is what other domains read of the runs domain: Updated, the
// event the domain appends to the outbox each time a run changes state,
// with UpdatedSchema its document's schema and Timer the run.timers rule
// it names when a timer moved the run; RunSources, what a run's end
// asks of the vcs domain, a back edge that domain implements; and
// StateAccess, which decides from a run's current state its StateGrant on
// a stack's managed state, the read, lock and write the state domain
// allows the run on its own stack, with who triggered the run, and the
// read alone on another's, which the access domain then decides as the
// run's stack.
//
// # Input and output
//
// None; it declares types, interfaces and a JSON Schema.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The domains above
// runs in the declared graph import it, the vcs domain implements
// RunSources, and the runs domain implements StateAccess.
package api
