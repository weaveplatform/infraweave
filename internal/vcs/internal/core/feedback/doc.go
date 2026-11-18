// Package feedback owns VCS feedback target selection and transaction-bound
// enqueueing for the events domain, and queued deployment reports to providers.
//
// # Input and output
//
// Kinds names run_updated through runsapi. Targets takes its outbox event.
// Enqueue validates a target and joins the active transaction; it creates a
// VCS job with an account-scoped delivery identity and performs no provider
// request. Deployment eligibility reads the runs domain's transition history
// through the event's exact sequence, rather than inferring reached phases
// from event delivery order or the run's current state.
//
// # Deployment ordering
//
// The first eligible transition fixes the run's credential-free binding and
// environment. A GitHub deployment is identified by the run payload. GitLab's
// native identity is host, project, environment, ref and SHA, so runs sharing it
// also share a queue ordering key and a newest-eligibility cursor. The first
// eligible transition, with creation time and ID as ties, orders these runs.
//
// A report owns a non-retrying transaction holding the account, job generation
// and resource row across its bounded provider effect. Cursor acknowledgement
// and fenced job completion commit together. Native lookup recovers an effect
// whose response or local acknowledgement was lost on a later queue attempt.
//
// # Dependencies
//
// It implements vcsapi.Feedback using runsapi, stacksapi, provider ports,
// platform/outbox, queue and tx.
// VCS assembly constructs it; events calls only its vcsapi port.
package feedback
