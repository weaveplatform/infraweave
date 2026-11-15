// Package control acts on runs at a caller's request: Service cancels a
// run that has not started through the lifecycle, needing
// RUN_CANCEL_BLOCKING as well for the run that holds its stack's run lock,
// confirms or discards a run waiting for confirmation, by default the one
// that holds its stack's run lock, sets the priority of a run that has
// not ended, and records a stop of a run that initializes or plans, or a
// kill of one a stop was asked of while a worker holds it, which notifies
// the run's pool so its worker's poll delivers it, each needing its
// blocking variant for the run that holds its stack's run lock. It
// replans a tracked run waiting for confirmation against the resource
// addresses kernel.CheckTargets accepts, keeping them for the
// specification of the run's next claim. It
// retries a proposed or tracked run that has ended, on its commit, with
// its overrides, needing RUN_RETRY_BLOCKING as well for a tracked run;
// promotes a proposed run whose commit is newer than its stack's tracked
// commit to a tracked run on it; discards each of a stack's runs that
// waits, each in a transaction of its own; and records a comment of 1 to
// MaxComment characters on a run. Each other action runs in one
// transaction that locks the run's row and answers the run, or the run
// it created, as a read.View; a *Refused names the run's state and what
// the action needs, and a *Blocked the blocking variant the caller lacks.
// Store is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store, the stacks domain
// through stacksapi.Stacks, the vcs domain's resolution of a commit
// through the stacksapi.StackSources BindSources binds, the access domain
// through accessapi.Authorizer and runs through the lifecycle.
//
// # Dependencies
//
// It imports accessapi, stacksapi, workersapi for the signals' names, the
// domain's lifecycle, machine and read, internal/kernel and platform/tx.
// The runs domain's assembly constructs it.
package control
