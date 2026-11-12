// Package core is the runs domain's core: Service, which answers the
// stack view's stacksapi.StackStatus, the run that holds each stack's run
// lock and the stack of a run, and is the domain's stacksapi.StackGuard,
// losing nothing on a stack's move and deleting a deleted stack's runs,
// with their transitions, and its run lock, tracked run and outputs in the
// delete's transaction. It is also the domain's runsapi.StateAccess,
// granting a run reads and a shared lock on its stack's state while it
// plans, and writes under an exclusive lock while it applies, destroys or
// performs a task, from the state it reads the run in on each call. Store
// is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store.
//
// # Dependencies
//
// It imports runsapi, stacksapi and internal/kernel. The runs domain's
// assembly constructs it over the domain's pg adapter.
package core
