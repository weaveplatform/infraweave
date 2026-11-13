// Package handoff is what the launcher hands the runner through the run
// container's standard input: the worker API's endpoint, the run, its run
// token and the state its preparation leads to, the run's specification
// with the secrets the launcher opened with the pool's private key, and
// the pool's public key, to which the runner seals a saved workspace.
// Passing it on standard input keeps the opened secrets out of the
// container's configuration, which Docker writes to the host's disk.
//
// Write encodes a Handoff; Read decodes one, refusing unknown members, a
// document over MaxSize and one without its endpoint, run, run token or
// state; First is the state the runner's first started report names.
//
// ref: decision 0008 § "The launcher and the runner"
//
// # Input and output
//
// Write writes one JSON document to an io.Writer and Read reads one from
// an io.Reader. Nothing is logged.
//
// # Dependencies
//
// It imports internal/agent/wire's models and the standard library. The
// launcher writes a handoff and the runner reads it.
package handoff
