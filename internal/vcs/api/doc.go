// Package api is what other domains call of the vcs domain: Sources,
// which resolves a branch, a tag or a commit of a stack's repository to a
// Commit, names the URL a worker fetches a commit's source from, hands a
// run the Credential it fetches with and revokes it when the run ends;
// ErrRefNotFound when the repository lacks the ref. Feedback exposes only
// event kinds, target selection and transaction-bound job enqueueing to
// the events domain; its payloads carry identifiers rather than secrets.
// Comments exposes queued account-bound Upsert for P18 notification policies,
// with no policy caller yet. Run-based plan comments remain a VCS decision.
//
// # Input and output
//
// None; it declares types and an interface.
//
// # Dependencies
//
// It imports the standard library, internal/kernel and platform/outbox. The vcs domain's
// assembly implements Sources; runs calls it through a back edge that
// internal/app binds.
package api
