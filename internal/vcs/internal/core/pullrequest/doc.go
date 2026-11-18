// Package pullrequest maintains provider request snapshots and head history,
// and resolves the requests whose recorded commits have runs on a stack.
// Upkeep joins the inbox processor's fenced completion transaction. It never
// creates a run; runs become associated dynamically by their commit SHA.
//
// # Input and output
//
// Inputs are verified normalized events, delivery timestamps and account-scoped
// stack settings. Preparation may resolve a missing original author's login
// through a provider outside the transaction. Reads page stored candidates and
// query Runs through its owned API, applying the fifty-request cap only after
// membership. The store owns SQL, retention and purge. No secrets are returned.
//
// # Dependencies
//
// This private VCS core package imports kernel, platform transactions, Runs and
// stacks APIs, and the VCS intake/integration ports. VCS assembly constructs it;
// adapters implement its store and provider author lookup.
package pullrequest
