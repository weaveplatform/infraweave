// Package api is what other domains call of the vcs domain: Sources,
// which resolves a branch, a tag or a commit of a stack's repository to a
// Commit and names the URL a worker fetches a commit's source from;
// ErrRefNotFound when the repository lacks the ref.
//
// # Input and output
//
// None; it declares types and an interface.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The vcs domain's
// assembly implements Sources; runs calls it through a back edge that
// internal/app binds.
package api
