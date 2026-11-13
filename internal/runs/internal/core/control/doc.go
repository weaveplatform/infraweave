// Package control acts on runs at a caller's request: Service cancels a
// run that has not started through the lifecycle, needing
// RUN_CANCEL_BLOCKING as well for the run that holds its stack's run lock,
// confirms or discards a run waiting for confirmation, by default the one
// that holds its stack's run lock, and sets the priority of a run that has
// not ended, each in one transaction that locks the run's row, answering
// the run as a read.View. Store is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store, the stacks domain
// through stacksapi.Stacks, the access domain through
// accessapi.Authorizer and runs through the lifecycle.
//
// # Dependencies
//
// It imports accessapi, stacksapi, the domain's lifecycle, machine and
// read, internal/kernel and platform/tx. The runs domain's assembly
// constructs it.
package control
