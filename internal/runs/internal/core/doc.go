// Package core is the runs domain's core: Service, which answers the
// stack view's stacksapi.StackStatus, the run that holds each stack's run
// lock and the stack of a run, and is the domain's stacksapi.StackGuard,
// losing nothing on a stack's move and deleting a deleted stack's runs,
// with their transitions, and its run lock, tracked run and outputs in the
// delete's transaction. Store is its storage port.
//
// # Input and output
//
// None of its own: it reaches storage through Store.
//
// # Dependencies
//
// It imports stacksapi and internal/kernel. The runs domain's assembly
// constructs it over the domain's pg adapter.
package core
