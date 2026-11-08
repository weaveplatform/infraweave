// Package space keeps each account's space tree (decision 0007 §
// "Spaces"). Seed is the tree an account starts with: its top space,
// whose ID and slug are the account's, so no space is named root and
// nothing sits above it, and legacy below it, an ordinary space with no
// suffix on its slug. A created space's slug is SlugOf its name and ULID
// and never changes. Service creates, moves and deletes spaces through
// Store, which keeps the closure table in the same transaction and locks
// the tree for each change: a move below the moved subtree, a delete of a
// space with children, of the top space or of legacy, and a move of the
// top space are each a *Refusal, as is a change a registered
// accessapi.SpaceGuard objects to. Who may change the tree is decided by
// authorization before a call reaches it.
//
// # Input and output
//
// None of its own: Store reads and writes the spaces and their closure
// rows, and each SpaceGuard reads its own domain's rows in the change's
// transaction.
//
// # Dependencies
//
// It imports accessapi, the access domain's account package,
// internal/kernel and the standard library. The access domain's adapters
// and assembly import it.
package space
