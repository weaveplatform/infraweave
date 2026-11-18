// Package feedback owns VCS feedback target selection and transaction-bound
// enqueueing for the events domain. Target selection remains empty until
// provider-specific feedback handlers are available.
//
// # Input and output
//
// Kinds names run_updated through runsapi. Targets takes its outbox event.
// Enqueue validates a target and joins the active transaction; it creates a
// VCS job with an account-scoped delivery identity and performs no provider
// request.
//
// # Dependencies
//
// It implements vcsapi.Feedback using runsapi, platform/outbox, queue and tx.
// VCS assembly constructs it; events calls only its vcsapi port.
package feedback
