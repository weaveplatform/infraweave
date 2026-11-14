// Package control acts on runs at a caller's request: Service cancels a
// run that has not started through the lifecycle, needing
// RUN_CANCEL_BLOCKING as well for the run that holds its stack's run lock,
// confirms or discards a run waiting for confirmation, by default the one
// that holds its stack's run lock, sets the priority of a run that has
// not ended, and records a stop of a run that initializes or plans, or a
// kill of one a stop was asked of while a worker holds it, which notifies
// the run's pool so its worker's poll delivers it, each needing its
// blocking variant for the run that holds its stack's run lock. Each runs
// in one transaction that locks the run's row and answers the run as a
// read.View; a *Refused names the run's state, and a *Blocked the
// blocking variant the caller lacks. Store is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store, the stacks domain
// through stacksapi.Stacks, the access domain through
// accessapi.Authorizer and runs through the lifecycle.
//
// # Dependencies
//
// It imports accessapi, stacksapi, workersapi for the signals' names, the
// domain's lifecycle, machine and read, internal/kernel and platform/tx. The runs domain's assembly
// constructs it.
package control
