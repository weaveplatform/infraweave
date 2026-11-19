// Package vcslab owns the scheduled VCS lab's verification resources.
// It is not part of infraweave's provider adapters or application wiring.
//
// # Input and output
//
// The lab uses its existing dedicated credentials to provision short-lived
// hooks and to inspect native feedback. GitHub's established signing secret is
// derived from its canonical App key; runtime hook updates never contain a
// secret. Private cleanup journals retain only restorable hook settings.
//
// # Dependencies
//
// The package uses the standard library and the repository's pinned GitHub
// client. Calls have caller-owned deadlines. Cleanup uses an independent bounded
// context, and a failed restore remains a failure rather than a skipped lab.
package vcslab
