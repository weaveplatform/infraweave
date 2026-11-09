// Package space keeps each account's space tree. Seed is the tree an account starts with: its top space,
// whose ID and slug are the account's, so no space is named root and
// nothing sits above it, and legacy below it, an ordinary space with no
// suffix on its slug. A created space's slug is SlugOf its name and ULID
// and never changes. Service creates, changes, moves and deletes spaces
// through Store, which keeps the closure table in the same transaction
// and locks the tree for each change: a move below the moved subtree, a
// delete of a space with children, of the top space or of legacy, a
// change of the top space's parent or inheritance, and a space below it
// left with no parent are each a *Refusal, as is a change a registered
// accessapi.SpaceGuard objects to. Service also decides who changes the
// tree beyond the route table's check: an account admin anything, and an
// admin of a space below the top, through Authorizer, only that space's
// name, description and labels while the account's
// allowNonAdminSpaceCreation, which Service reads and toggles, is off;
// while it is on, also creates below, moves between and deletes the
// spaces they administer, creating only spaces that inherit and never
// stopping one inheriting. A change it withholds is a *Denied naming
// SPACE_ADMIN. After each change it drops this replica's authorization
// snapshot of the account through Deps.Drop, so the change's answer and
// the caller's next request here see it. A space with no name is
// ErrName, and labels breaking kernel.CheckLabels' rule, a repeat
// included, are a *kernel.Invalid naming each at its pointer; labels keep
// the order given. A Space's Version is raised by every change; Update
// and Delete carry a kernel.Expect checked against the space read under
// the tree's lock, after the caller's right to the change, and a version
// it does not accept is kernel.ErrStale, changing nothing. Search and
// Suggest read the spaces a caller may read through Store by Fields, the reference's
// generic search over the tree, with the reachable spaces the caller
// passes held in the query.
//
// ref: decision 0007 § "Spaces"
// ref: decision 0022
//
// # Input and output
//
// None of its own: Store reads and writes the spaces, their closure rows
// and the setting, and each SpaceGuard reads its own domain's rows in the
// change's transaction.
//
// # Dependencies
//
// It imports accessapi, the access domain's account package,
// internal/kernel, kernel/search and the standard library. The access domain's adapters
// and assembly import it.
package space
