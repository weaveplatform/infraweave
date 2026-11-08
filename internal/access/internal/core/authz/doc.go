// Package authz is decision 0007's evaluator. Build makes an account's
// Snapshot from its authorization rows at one authz_version: the space
// tree with each space's parent and inherit_entities, the roles with
// their actions (a system role's from role.SystemActions), the bindings
// by actor and the managed user groups by name. A snapshot never changes
// once built. Its Authorize decides whether a principal, as a Who, may
// perform an action in a space: an account admin, who holds space-admin
// on the top space, may do every action; otherwise the actor set (a user
// and the groups its teams name, an API key and its teams, a personal
// key's owner, or a run's stack with an implicit space-reader on its home)
// is matched against resident grants on the space, propagated grants on
// its ancestors and inherited grants on descendants whose path up to it
// inherits, each inherited grant contributing space-reader's actions
// only. RunDenied refuses a run principal the state, run, task and log
// actions whatever it holds; ORGANIZATION_ADMIN is never an account's to
// grant and AUTHENTICATED is any principal of the account. Reachable is
// every space where Authorize allows an action, for list filters, and
// Explain every grant reaching a space with its source and binding.
//
// Service holds one snapshot per account, built on first use, and
// implements accessapi.Authorizer. Drop forgets an account's snapshot on
// its authz:<account> message, Flush forgets them all after a Resync,
// and Refresh drops each one whose account's version moved, for a lost
// message; a load that a drop overtakes is used once and not kept. A
// check of a space the snapshot lacks reads the account's version first
// and rebuilds the snapshot when it has moved, as the space is newer than
// the snapshot. A run
// principal holds no grant until its stack and home space reach the
// principal.
//
// # Input and output
//
// None: a Store reads the rows, which the access domain's pg adapter
// implements.
//
// # Dependencies
//
// It imports accessapi for the action catalogue and the Authorizer port,
// the role package, internal/kernel and the standard library. The access
// domain's pg adapter and assembly import it.
package authz
