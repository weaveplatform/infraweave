// Package api is what other domains read of the cloud domain: Credentials,
// the port a run's phase reads its cloud credentials through, with
// PhaseRequest naming the phase by its Scope and Phase holding the
// Variable list it receives.
//
// # Input and output
//
// None; it declares types and an interface.
//
// # Dependencies
//
// It imports internal/kernel.
package api
